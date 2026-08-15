// Copyright 2020 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"crypto/tls"
	"flag"
	"sort"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/hibiken/asynq/internal/log"
	h "github.com/hibiken/asynq/internal/testutil"
	"github.com/redis/go-redis/v9"
)

//============================================================================
// This file defines helper functions and variables used in other test files.
//============================================================================

// variables used for package testing.
var (
	redisAddr string
	redisDB   int

	useRedisCluster   bool
	redisClusterAddrs string // comma-separated list of host:port

	testLogLevel = FatalLevel
)

var testLogger *log.Logger

func init() {
	flag.StringVar(&redisAddr, "redis_addr", "localhost:6379", "redis address to use in testing")
	flag.IntVar(&redisDB, "redis_db", 14, "redis db number to use in testing")
	flag.BoolVar(&useRedisCluster, "redis_cluster", false, "use redis cluster as a broker in testing")
	flag.StringVar(&redisClusterAddrs, "redis_cluster_addrs", "localhost:7000,localhost:7001,localhost:7002", "comma separated list of redis server addresses")
	flag.Var(&testLogLevel, "loglevel", "log level to use in testing")

	testLogger = log.NewLogger(nil)
	testLogger.SetLevel(toInternalLogLevel(testLogLevel))
}

func setup(tb testing.TB) (r redis.UniversalClient) {
	tb.Helper()
	if useRedisCluster {
		addrs := strings.Split(redisClusterAddrs, ",")
		if len(addrs) == 0 {
			tb.Fatal("No redis cluster addresses provided. Please set addresses using --redis_cluster_addrs flag.")
		}
		r = redis.NewClusterClient(&redis.ClusterOptions{
			Addrs: addrs,
		})
	} else {
		r = redis.NewClient(&redis.Options{
			Addr: redisAddr,
			DB:   redisDB,
		})
	}
	// Start each test with a clean slate.
	h.FlushDB(tb, r)
	return r
}

func getRedisConnOpt(tb testing.TB) RedisConnOpt {
	tb.Helper()
	if useRedisCluster {
		addrs := strings.Split(redisClusterAddrs, ",")
		if len(addrs) == 0 {
			tb.Fatal("No redis cluster addresses provided. Please set addresses using --redis_cluster_addrs flag.")
		}
		return RedisClusterClientOpt{
			Addrs: addrs,
		}
	}
	return RedisClientOpt{
		Addr: redisAddr,
		DB:   redisDB,
	}
}

var sortTaskOpt = cmp.Transformer("SortMsg", func(in []*Task) []*Task {
	out := append([]*Task(nil), in...) // Copy input to avoid mutating it
	sort.Slice(out, func(i, j int) bool {
		return out[i].Type() < out[j].Type()
	})
	return out
})

func TestParseRedisURI(t *testing.T) {
	tests := []struct {
		uri  string
		want RedisConnOpt
	}{
		{
			"redis://localhost:6379",
			RedisClientOpt{Addr: "localhost:6379"},
		},
		{
			"rediss://localhost:6379",
			RedisClientOpt{Addr: "localhost:6379", TLSConfig: &tls.Config{ServerName: "localhost"}},
		},
		{
			"redis://localhost:6379/3",
			RedisClientOpt{Addr: "localhost:6379", DB: 3},
		},
		{
			"redis://:mypassword@localhost:6379",
			RedisClientOpt{Addr: "localhost:6379", Password: "mypassword"},
		},
		{
			"redis://:mypassword@127.0.0.1:6379/11",
			RedisClientOpt{Addr: "127.0.0.1:6379", Password: "mypassword", DB: 11},
		},
		{
			"redis-socket:///var/run/redis/redis.sock",
			RedisClientOpt{Network: "unix", Addr: "/var/run/redis/redis.sock"},
		},
		{
			"redis-socket://:mypassword@/var/run/redis/redis.sock",
			RedisClientOpt{Network: "unix", Addr: "/var/run/redis/redis.sock", Password: "mypassword"},
		},
		{
			"redis-socket:///var/run/redis/redis.sock?db=7",
			RedisClientOpt{Network: "unix", Addr: "/var/run/redis/redis.sock", DB: 7},
		},
		{
			"redis-socket://:mypassword@/var/run/redis/redis.sock?db=12",
			RedisClientOpt{Network: "unix", Addr: "/var/run/redis/redis.sock", Password: "mypassword", DB: 12},
		},
		{
			"redis-sentinel://localhost:5000,localhost:5001,localhost:5002?master=mymaster",
			RedisFailoverClientOpt{
				MasterName:    "mymaster",
				SentinelAddrs: []string{"localhost:5000", "localhost:5001", "localhost:5002"},
			},
		},
		{
			"redis-sentinel://:mypassword@localhost:5000,localhost:5001,localhost:5002?master=mymaster",
			RedisFailoverClientOpt{
				MasterName:       "mymaster",
				SentinelAddrs:    []string{"localhost:5000", "localhost:5001", "localhost:5002"},
				SentinelPassword: "mypassword",
			},
		},
	}

	for _, tc := range tests {
		got, err := ParseRedisURI(tc.uri)
		if err != nil {
			t.Errorf("ParseRedisURI(%q) returned an error: %v", tc.uri, err)
			continue
		}

		if diff := cmp.Diff(tc.want, got, cmpopts.IgnoreUnexported(tls.Config{})); diff != "" {
			t.Errorf("ParseRedisURI(%q) = %+v, want %+v\n(-want,+got)\n%s", tc.uri, got, tc.want, diff)
		}
	}
}

func TestParseRedisURIErrors(t *testing.T) {
	tests := []struct {
		desc string
		uri  string
	}{
		{
			"unsupported scheme",
			"rdb://localhost:6379",
		},
		{
			"missing scheme",
			"localhost:6379",
		},
		{
			"multiple db numbers",
			"redis://localhost:6379/1,2,3",
		},
		{
			"missing path for socket connection",
			"redis-socket://?db=one",
		},
		{
			"non integer for db numbers for socket",
			"redis-socket:///some/path/to/redis?db=one",
		},
	}

	for _, tc := range tests {
		_, err := ParseRedisURI(tc.uri)
		if err == nil {
			t.Errorf("%s: ParseRedisURI(%q) succeeded for malformed input, want error",
				tc.desc, tc.uri)
		}
	}
}
