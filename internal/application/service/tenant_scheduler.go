package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

type cfsScheduler struct {
	redisClient  *redis.Client
	taskEnqueuer interfaces.TaskEnqueuer
}

// envScript 用于在 Redis 中原子地将任务添加到租户专有的 List 中，并更新全局 ZSET 中的 vruntime
const envScript = `
-- KEYS[1]: 租户专有的Task List (cfs:tenant:{tenantID})
-- KEYS[2]: 全局ZSET,记录用户当前vruntime(cfs:vruntime)
-- ARGV[1]: 序列化的task json
-- ARGV[2]: tenantID

redis.call("LPUSH",KEYS[1],ARGV[1])
local score = redis.call("ZSCORE",KEYS[2],ARGV[2])
--如果是新加入的用户,将其vruntime初始化为当前最小值--
if not score then
	local min_vruntime = 0
	local min_entry = redis.call("ZRANGE",KEYS[2],0,0,"WITHSCORES")
	if min_entry and #min_entry == 2 then
		min_vruntime = tonumber(min_entry[2])
	end
	redis.call("ZADD",KEYS[2],min_vruntime,ARGV[2])
end
`

func NewCFSScheduler(redisClient *redis.Client, taskEnqueuer interfaces.TaskEnqueuer) interfaces.TenantFairScheduler {
	return &cfsScheduler{
		redisClient:  redisClient,
		taskEnqueuer: taskEnqueuer,
	}
}

func (c *cfsScheduler) SubmitTask(ctx context.Context, tenantID uint64, taskType string, payload []byte, cost int64, opts *interfaces.CfsTaskOptions) error {
	// 将任务包装成 cfsTaskWrapper 结构体
	wrapper := interfaces.CfsTaskWrapper{
		TaskType: taskType,
		Payload:  payload,
		Cost:     cost,
		Options:  opts,
	}
	data, err := json.Marshal(wrapper)
	if err != nil {
		logger.Errorf(ctx, "[CFS scheduler] Failed to marshal task wrapper: %s: %v", wrapper.TaskType, err)
		return err
	}
	listKey := fmt.Sprintf("cfs:tenant:%d", tenantID)
	zsetKet := "cfs:vruntime"
	// 使用 Lua 脚本原子地将任务添加到租户专有的 List 中，并更新全局 ZSET 中的 vruntime
	err = c.redisClient.Eval(ctx, envScript, []string{listKey, zsetKet}, data, tenantID).Err()
	if err != nil {
		logger.Errorf(ctx, "[CFS scheduler] Lua script exec failed for tenant %d: %v", tenantID, err)
		return err
	}
	logger.Debugf(ctx, "[CFS scheduler] Task submitted: type=%s, tenant=%d, cost=%d", wrapper.TaskType, tenantID, cost)
	return nil
}
func (c *cfsScheduler) Start(ctx context.Context) {
	go func() {
		logger.Infof(ctx, "[CFS scheduler] Background loop started")
		zsetKey := "cfs:vruntime"
		for {
			select {
			case <-ctx.Done():
				logger.Infof(ctx, "[CFS scheduler] Background loop exiting due to context cancellation")
				return
			default:
			}
			// 从全局 ZSET 中获取 vruntime 最小的租户ID
			res, err := c.redisClient.ZRangeWithScores(ctx, zsetKey, 0, 0).Result()
			if err != nil || len(res) == 0 {
				time.Sleep(100 * time.Millisecond)
				continue
			}
			tenantID, ok := res[0].Member.(string)
			if !ok {
				logger.Errorf(ctx, "[CFS scheduler] Invalid member type in ZSET: %v", res[0].Member)
				c.redisClient.ZRem(ctx, zsetKey, res[0].Member)
				continue
			}
			listKey := fmt.Sprintf("cfs:tenant:%s", tenantID)
			taskStr, err := c.redisClient.LPop(ctx, listKey).Result()
			if err == redis.Nil {
				c.redisClient.ZRem(ctx, zsetKey, tenantID)
				continue
			} else if err != nil {
				logger.Errorf(ctx, "[CFS scheduler] Failed to LPOP task for tenant %s: %v", listKey, err)
				time.Sleep(100 * time.Millisecond)
				continue
			}
			// 反序列化任务包装
			var wrapper interfaces.CfsTaskWrapper
			if err = json.Unmarshal([]byte(taskStr), &wrapper); err != nil {
				logger.Errorf(ctx, "[CFS scheduler] Failed to unmarshal task wrapper for tenant %s: %v", tenantID, err)
				continue
			}
			// 根据 wrapper.Options 中的配置构建 asynq.Task 的选项，并将任务提交到 asynq 队列中
			var asynqOpts []asynq.Option
			if wrapper.Options != nil {
				if wrapper.Options.Queue != "" {
					asynqOpts = append(asynqOpts, asynq.Queue(wrapper.Options.Queue))
				} else {
					asynqOpts = append(asynqOpts, asynq.Queue("default"))
				}

				if wrapper.Options.MaxRetry > 0 {
					asynqOpts = append(asynqOpts, asynq.MaxRetry(wrapper.Options.MaxRetry))
				}

				if wrapper.Options.TaskID != "" {
					asynqOpts = append(asynqOpts, asynq.TaskID(wrapper.Options.TaskID))
				}
				//如果有其他asynq配置需求，请在 CfsTaskOptions 中添加字段，并在这里进行转换和添加
			} else {
				asynqOpts = append(asynqOpts, asynq.Queue("default"), asynq.MaxRetry(3))
			}

			asynqTask := asynq.NewTask(wrapper.TaskType, wrapper.Payload, asynqOpts...)
			_, err = c.taskEnqueuer.Enqueue(asynqTask)
			if err != nil {
				logger.Errorf(ctx, "[CFS scheduler] Failed to enqueue task for tenant %s: %v", tenantID, err)
			} else {
				logger.Debugf(ctx, "[CFS scheduler] Enqueued task for tenant %s: type=%s cost=%d", tenantID, wrapper.TaskType, wrapper.Cost)
			}

			// 更新全局 ZSET 中该租户的 vruntime，增加的值等于任务的 cost
			c.redisClient.ZIncrBy(ctx, zsetKey, float64(wrapper.Cost), tenantID)
		}
	}()

}
