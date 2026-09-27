package sandbox

import (
	"reflect"
	"testing"
)

func TestArgsRoundTrip(t *testing.T) {
	ports := []int{8080, 41234}
	command := []string{"/usr/bin/python3", "-s", "-u", "main.py", "--", "x=y"}
	args := Args(ports, command)
	if args[0] != Subcommand {
		t.Fatalf("args = %v", args)
	}
	gotPorts, gotCommand, err := parseArgs(args[1:])
	if err != nil || !reflect.DeepEqual(gotPorts, ports) || !reflect.DeepEqual(gotCommand, command) {
		t.Fatalf("parsed %v %v %v", gotPorts, gotCommand, err)
	}
	for _, bad := range [][]string{
		{},
		{"--"},
		{"--listen"},
		{"--listen", "x", "--", "a"},
		{"--listen", "0", "--", "a"},
		{"--listen", "70000", "--", "a"},
		{"--bogus"},
	} {
		if _, _, err := parseArgs(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
