package router

import (
	"log"
	"os"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"go.uber.org/dig"
)

type AsynqTaskParams struct {
	dig.In

	Server    *asynq.Server
	Extracter interfaces.Extracter
}

func getAsynqRedisClientOpt() *asynq.RedisClientOpt {
	opt := &asynq.RedisClientOpt{
		Addr:         os.Getenv("REDIS_ADDR"),
		Password:     os.Getenv("REDIS_PASSWORD"),
		ReadTimeout:  100 * time.Millisecond,
		WriteTimeout: 200 * time.Millisecond,
		DB:           0,
	}
	return opt
}

func NewAsyncqClient() *asynq.Client {
	opt := getAsynqRedisClientOpt()
	client := asynq.NewClient(opt)
	return client
}

func NewAsynqServer() *asynq.Server {
	opt := getAsynqRedisClientOpt()
	srv := asynq.NewServer(
		opt,
		asynq.Config{
			Queues: map[string]int{
				"critical": 6, // Highest priority queue
				"default":  3, // Default priority queue
				"low":      1, // Lowest priority queue
			},
		},
	)
	return srv
}

func RunAsynqServer(params AsynqTaskParams) *asynq.ServeMux {
	// Create a new mux and register all handlers
	mux := asynq.NewServeMux()

	mux.HandleFunc(types.TypeChunkExtract, params.Extracter.Extract)

	go func() {
		// Start the server
		if err := params.Server.Run(mux); err != nil {
			log.Fatalf("could not run server: %v", err)
		}
	}()
	return mux
}
