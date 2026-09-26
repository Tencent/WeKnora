package sandbox

import (
	"reflect"
	"testing"
)

func TestArgsRoundTrip(t *testing.T) {
	relays := []Relay{{Port: 8080, Socket: "/tmp/p/hostapi.sock"}, {Port: 41234, Socket: "/tmp/p/proxy.sock"}}
	command := []string{"/usr/bin/python3", "-s", "-u", "main.py", "--", "x=y"}
	args := Args(relays, command)
	if args[0] != Subcommand {
		t.Fatalf("args = %v", args)
	}
	gotRelays, gotCommand, err := parseArgs(args[1:])
	if err != nil || !reflect.DeepEqual(gotRelays, relays) || !reflect.DeepEqual(gotCommand, command) {
		t.Fatalf("parsed %v %v %v", gotRelays, gotCommand, err)
	}
	for _, bad := range [][]string{
		{},
		{"--"},
		{"--relay"},
		{"--relay", "x=/s", "--", "a"},
		{"--relay", "80", "--", "a"},
		{"--relay", "70000=/s", "--", "a"},
		{"--bogus"},
	} {
		if _, _, err := parseArgs(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
