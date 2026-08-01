// Copyright 2022 Kentaro Hibino. All rights reserved.
// Use of this source code is governed by a MIT license
// that can be found in the LICENSE file.

package asynq

import (
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

// Trivial implementation of PeriodicTaskConfigProvider for testing purpose.
type FakeConfigProvider struct {
	mu   sync.Mutex
	cfgs []*PeriodicTaskConfig
}

func (p *FakeConfigProvider) SetConfigs(cfgs []*PeriodicTaskConfig) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cfgs = cfgs
}

func (p *FakeConfigProvider) GetConfigs() ([]*PeriodicTaskConfig, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cfgs, nil
}

func TestNewPeriodicTaskManager(t *testing.T) {
	redisConnOpt := getRedisConnOpt(t)
	cfgs := []*PeriodicTaskConfig{
		{Cronspec: "* * * * *", Task: NewTask("foo", nil)},
		{Cronspec: "* * * * *", Task: NewTask("bar", nil)},
	}
	tests := []struct {
		desc string
		opts PeriodicTaskManagerOpts
	}{
		{
			desc: "with provider and redisConnOpt",
			opts: PeriodicTaskManagerOpts{
				RedisConnOpt:               redisConnOpt,
				PeriodicTaskConfigProvider: &FakeConfigProvider{cfgs: cfgs},
			},
		},
		{
			desc: "with sync option",
			opts: PeriodicTaskManagerOpts{
				RedisConnOpt:               redisConnOpt,
				PeriodicTaskConfigProvider: &FakeConfigProvider{cfgs: cfgs},
				SyncInterval:               5 * time.Minute,
			},
		},
		{
			desc: "with scheduler option",
			opts: PeriodicTaskManagerOpts{
				RedisConnOpt:               redisConnOpt,
				PeriodicTaskConfigProvider: &FakeConfigProvider{cfgs: cfgs},
				SyncInterval:               5 * time.Minute,
				SchedulerOpts: &SchedulerOpts{
					LogLevel: DebugLevel,
				},
			},
		},
	}

	for _, tc := range tests {
		_, err := NewPeriodicTaskManager(tc.opts)
		if err != nil {
			t.Errorf("%s; NewPeriodicTaskManager returned error: %v", tc.desc, err)
		}
	}

	t.Run("error", func(t *testing.T) {
		tests := []struct {
			desc string
			opts PeriodicTaskManagerOpts
		}{
			{
				desc: "without provider",
				opts: PeriodicTaskManagerOpts{
					RedisConnOpt: redisConnOpt,
				},
			},
			{
				desc: "without redisConOpt",
				opts: PeriodicTaskManagerOpts{
					PeriodicTaskConfigProvider: &FakeConfigProvider{cfgs: cfgs},
				},
			},
		}

		for _, tc := range tests {
			_, err := NewPeriodicTaskManager(tc.opts)
			if err == nil {
				t.Errorf("%s; NewPeriodicTaskManager did not return error", tc.desc)
			}
		}
	})
}

func TestPeriodicTaskConfigHash(t *testing.T) {
	tests := []struct {
		desc   string
		a      *PeriodicTaskConfig
		b      *PeriodicTaskConfig
		isSame bool
	}{
		{
			desc: "basic identity test",
			a: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", nil),
			},
			b: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", nil),
			},
			isSame: true,
		},
		{
			desc: "with a option",
			a: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", nil),
				Opts:     []Option{Queue("myqueue")},
			},
			b: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", nil),
				Opts:     []Option{Queue("myqueue")},
			},
			isSame: true,
		},
		{
			desc: "with multiple options (different order)",
			a: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", nil),
				Opts:     []Option{Unique(5 * time.Minute), Queue("myqueue")},
			},
			b: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", nil),
				Opts:     []Option{Queue("myqueue"), Unique(5 * time.Minute)},
			},
			isSame: true,
		},
		{
			desc: "with payload",
			a: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", []byte("hello world!")),
				Opts:     []Option{Queue("myqueue")},
			},
			b: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", []byte("hello world!")),
				Opts:     []Option{Queue("myqueue")},
			},
			isSame: true,
		},
		{
			desc: "with different cronspecs",
			a: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", nil),
			},
			b: &PeriodicTaskConfig{
				Cronspec: "5 * * * *",
				Task:     NewTask("foo", nil),
			},
			isSame: false,
		},
		{
			desc: "with different task type",
			a: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", nil),
			},
			b: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("bar", nil),
			},
			isSame: false,
		},
		{
			desc: "with different options",
			a: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", nil),
				Opts:     []Option{Queue("myqueue")},
			},
			b: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", nil),
				Opts:     []Option{Unique(10 * time.Minute)},
			},
			isSame: false,
		},
		{
			desc: "with different options (one is subset of the other)",
			a: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", nil),
				Opts:     []Option{Queue("myqueue")},
			},
			b: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", nil),
				Opts:     []Option{Queue("myqueue"), Unique(10 * time.Minute)},
			},
			isSame: false,
		},
		{
			desc: "with different payload",
			a: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", []byte("hello!")),
				Opts:     []Option{Queue("myqueue")},
			},
			b: &PeriodicTaskConfig{
				Cronspec: "* * * * *",
				Task:     NewTask("foo", []byte("HELLO!")),
				Opts:     []Option{Queue("myqueue"), Unique(10 * time.Minute)},
			},
			isSame: false,
		},
	}

	for _, tc := range tests {
		if tc.isSame && tc.a.hash() != tc.b.hash() {
			t.Errorf("%s: a.hash=%s b.hash=%s expected to be equal",
				tc.desc, tc.a.hash(), tc.b.hash())
		}
		if !tc.isSame && tc.a.hash() == tc.b.hash() {
			t.Errorf("%s: a.hash=%s b.hash=%s expected to be not equal",
				tc.desc, tc.a.hash(), tc.b.hash())
		}
	}
}

// Things to test.
// - Run the manager
// - Change provider to return new configs
// - Verify that the scheduler synced with the new config
func TestPeriodicTaskManager(t *testing.T) {
	// Note: In this test, we'll use task type as an ID for each config.
	cfgs := []*PeriodicTaskConfig{
		{Task: NewTask("task1", nil), Cronspec: "* * * * 1"},
		{Task: NewTask("task2", nil), Cronspec: "* * * * 2"},
	}
	const syncInterval = 3 * time.Second
	provider := &FakeConfigProvider{cfgs: cfgs}
	mgr, err := NewPeriodicTaskManager(PeriodicTaskManagerOpts{
		RedisConnOpt:               getRedisConnOpt(t),
		PeriodicTaskConfigProvider: provider,
		SyncInterval:               syncInterval,
	})
	if err != nil {
		t.Fatalf("Failed to initialize PeriodicTaskManager: %v", err)
	}

	if err := mgr.Start(); err != nil {
		t.Fatalf("Failed to start PeriodicTaskManager: %v", err)
	}
	defer mgr.Shutdown()

	got := extractCronEntries(mgr.s)
	want := []*cronEntry{
		{Cronspec: "* * * * 1", TaskType: "task1"},
		{Cronspec: "* * * * 2", TaskType: "task2"},
	}
	if diff := cmp.Diff(want, got, sortCronEntry); diff != "" {
		t.Errorf("Diff found in scheduler's registered entries: %s", diff)
	}

	// Change the underlying configs
	// - task2 removed
	// - task3 added
	provider.SetConfigs([]*PeriodicTaskConfig{
		{Task: NewTask("task1", nil), Cronspec: "* * * * 1"},
		{Task: NewTask("task3", nil), Cronspec: "* * * * 3"},
	})

	// Wait for the next sync
	time.Sleep(syncInterval * 2)

	// Verify the entries are synced
	got = extractCronEntries(mgr.s)
	want = []*cronEntry{
		{Cronspec: "* * * * 1", TaskType: "task1"},
		{Cronspec: "* * * * 3", TaskType: "task3"},
	}
	if diff := cmp.Diff(want, got, sortCronEntry); diff != "" {
		t.Errorf("Diff found in scheduler's registered entries: %s", diff)
	}

	// Change the underlying configs
	// All configs removed, empty set.
	provider.SetConfigs([]*PeriodicTaskConfig{})

	// Wait for the next sync
	time.Sleep(syncInterval * 2)

	// Verify the entries are synced
	got = extractCronEntries(mgr.s)
	want = []*cronEntry{}
	if diff := cmp.Diff(want, got, sortCronEntry); diff != "" {
		t.Errorf("Diff found in scheduler's registered entries: %s", diff)
	}
}

func extractCronEntries(s *Scheduler) []*cronEntry {
	var out []*cronEntry
	for _, e := range s.cron.Entries() {
		job := e.Job.(*enqueueJob)
		out = append(out, &cronEntry{Cronspec: job.cronspec, TaskType: job.task.Type()})
	}
	return out
}

var sortCronEntry = cmp.Transformer("sortCronEntry", func(in []*cronEntry) []*cronEntry {
	out := append([]*cronEntry(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].TaskType < out[j].TaskType
	})
	return out
})

// A simple struct to allow for simpler comparison in test.
type cronEntry struct {
	Cronspec string
	TaskType string
}
