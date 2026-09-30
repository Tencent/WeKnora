package mcp

import (
	"testing"
	"time"
)

func TestDynamicIdleRetirementDoesNotInterruptActiveOperations(t *testing.T) {
	c := &managedMCPClient{dynamic: true, lastUsed: time.Now().Add(-time.Hour)}
	if err := c.begin(); err != nil {
		t.Fatal(err)
	}
	if c.retireIdle(time.Now().Add(time.Hour)) {
		t.Fatal("retired an active operation")
	}
	c.end()
	if c.retireIdle(time.Now()) {
		t.Fatal("retired a recently used connection")
	}
	if !c.retireIdle(time.Now().Add(3 * time.Minute)) {
		t.Fatal("did not retire idle dynamic connection")
	}
	if c.begin() == nil {
		t.Fatal("retired connection accepted an operation")
	}
	static := &managedMCPClient{lastUsed: time.Now().Add(-time.Hour)}
	if static.retireIdle(time.Now()) {
		t.Fatal("static connection must keep existing lifetime")
	}
}

func TestDynamicIdleRetirementUsesTwoMinuteThreshold(t *testing.T) {
	now := time.Now()
	c := &managedMCPClient{dynamic: true, lastUsed: now}
	if c.retireIdle(now.Add(time.Minute)) {
		t.Fatal("retired connection before two minutes")
	}
	if !c.retireIdle(now.Add(2 * time.Minute)) {
		t.Fatal("did not retire connection at two minutes")
	}
}
