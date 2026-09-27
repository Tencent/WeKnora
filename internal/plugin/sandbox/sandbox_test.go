package sandbox

import (
	"reflect"
	"testing"
)

func TestArgsRoundTrip(t *testing.T) {
	spec := Spec{
		Netns: true, Ports: []int{8080, 41234},
		Landlock: true, Read: []string{"/usr", "/opt/plugin dir"}, Write: []string{"/tmp/p"},
		LimitTCP: true, ConnectPorts: []int{41234},
	}
	command := []string{"/usr/bin/python3", "-s", "-u", "main.py", "--", "--read", "x=y"}
	args := Args(spec, command)
	if args[0] != Subcommand {
		t.Fatalf("args = %v", args)
	}
	gotSpec, gotCommand, err := parseArgs(args[1:])
	if err != nil || !reflect.DeepEqual(gotSpec, spec) || !reflect.DeepEqual(gotCommand, command) {
		t.Fatalf("parsed %+v %v %v", gotSpec, gotCommand, err)
	}
	if got, _, err := parseArgs(Args(Spec{}, command)[1:]); err != nil || !reflect.DeepEqual(got, Spec{}) {
		t.Fatalf("an empty spec parsed as %+v, %v", got, err)
	}
	for _, bad := range [][]string{
		{},
		{"--"},
		{"--listen"},
		{"--read"},
		{"--listen", "x", "--", "a"},
		{"--listen", "0", "--", "a"},
		{"--connect", "70000", "--", "a"},
		{"--bogus"},
	} {
		if _, _, err := parseArgs(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
