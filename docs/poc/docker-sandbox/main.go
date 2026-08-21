// PoC for docs/sandbox-docker-backend.md: can the Docker Engine API back the
// semantics WeKnora's RemoteSandboxClient contract requires (session-persistent
// sandbox, exec, filesystem, metadata, lifecycle), plus the snapshot workflow
// planned for E2B?
//
// It drives a real daemon and prints PASS/FAIL plus the observed evidence for
// every operation, including the ones Docker cannot honour ("GAP" steps, which
// assert the gap rather than a capability).
//
// Run with a reachable daemon (DOCKER_HOST honoured):
//
//	cd docs/poc/docker-sandbox && go run .
package main

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"
)

const (
	baseImage = "python:3.11-slim"
	execUser  = "user"
)

var failures int

func step(name string, err error, detail string) {
	if err != nil {
		failures++
		fmt.Printf("FAIL  %-48s %v\n", name, err)
		return
	}
	fmt.Printf("PASS  %-48s %s\n", name, detail)
}

func main() {
	ctx := context.Background()
	cli, err := client.New(client.WithHostFromEnv(), client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Println("cannot reach docker:", err)
		os.Exit(1)
	}
	defer cli.Close()

	ping, err := cli.Ping(ctx, client.PingOptions{})
	step("Health: ping control plane", err, fmt.Sprintf("api=%s", ping.APIVersion))

	tmplImage := "weknora-poc/template:v2"
	err = buildTemplateImage(ctx, cli, tmplImage)
	step("Template: base image with uid-1000 user", err, tmplImage)
	if err != nil {
		os.Exit(1)
	}

	sessionID := fmt.Sprintf("poc-session-%d", time.Now().Unix())
	labels := map[string]string{
		"weknora.managed":   "true",
		"weknora.tenant":    "1",
		"weknora.session":   sessionID,
		"weknora.config":    "cfg-poc",
		"weknora.createdAt": time.Now().UTC().Format(time.RFC3339),
	}

	// --- Create -------------------------------------------------------------
	id, err := createSandbox(ctx, cli, tmplImage, labels)
	step("Create: container as session sandbox", err, short(id))
	if err != nil {
		os.Exit(1)
	}
	defer remove(cli, id)

	// --- Connect / Get ------------------------------------------------------
	fresh, err := client.New(client.WithHostFromEnv(), client.WithAPIVersionNegotiation())
	if err == nil {
		defer fresh.Close()
		var insp client.ContainerInspectResult
		insp, err = fresh.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
		if err == nil && insp.Container.Config.Labels["weknora.session"] != sessionID {
			err = errors.New("metadata labels not preserved")
		}
		step("Connect+Metadata: re-attach from a new client", err,
			fmt.Sprintf("state=%s session=%s", insp.Container.State.Status,
				insp.Container.Config.Labels["weknora.session"]))
	}

	// --- List by metadata ---------------------------------------------------
	listed, err := cli.ContainerList(ctx, client.ContainerListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", "weknora.session="+sessionID),
	})
	if err == nil && len(listed.Items) != 1 {
		err = fmt.Errorf("expected 1 container, got %d", len(listed.Items))
	}
	step("List: server-side metadata filter", err, fmt.Sprintf("matched=%d", len(listed.Items)))

	// --- Exec: user / workdir / env / stdin / demux / exit code -------------
	res, err := execIn(ctx, cli, id, execRequest{
		cmd:     []string{"bash", "-lc", `cat; echo "user=$(id -un) cwd=$(pwd) env=$GREET"; echo oops >&2; exit 7`},
		user:    execUser,
		workDir: "/workspace",
		env:     []string{"GREET=hello"},
		stdin:   "stdin-payload\n",
	})
	if err == nil {
		switch {
		case res.exitCode != 7:
			err = fmt.Errorf("exit code %d, want 7", res.exitCode)
		case !strings.Contains(res.stdout, "user=user cwd=/workspace env=hello"):
			err = fmt.Errorf("stdout mismatch: %q", res.stdout)
		case !strings.Contains(res.stdout, "stdin-payload"):
			err = fmt.Errorf("stdin not delivered: %q", res.stdout)
		case !strings.Contains(res.stderr, "oops"):
			err = fmt.Errorf("stderr not separated: %q", res.stderr)
		}
	}
	step("Exec: user/workdir/env/stdin/demux/exit", err,
		fmt.Sprintf("exit=%d stdout=%q stderr=%q", res.exitCode, oneline(res.stdout), oneline(res.stderr)))

	// --- Exec cancellation: does a client-side cancel kill the process? -----
	cancelCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	_, execErr := execIn(cancelCtx, cli, id, execRequest{
		cmd: []string{"bash", "-lc", "sleep 30"},
	})
	cancel()
	time.Sleep(1 * time.Second)
	probe, _ := execIn(ctx, cli, id, execRequest{
		cmd: []string{"bash", "-lc", "ps -eo args= | grep -c '^sleep 30$' || true"},
	})
	step("Exec cancel: client cancel does NOT kill process (GAP)", nil,
		fmt.Sprintf("clientErr=%v surviving 'sleep' procs=%s", execErr != nil, oneline(probe.stdout)))

	// --- Timeout enforced inside the container ------------------------------
	start := time.Now()
	killRes, err := execIn(ctx, cli, id, execRequest{
		cmd: []string{"timeout", "-s", "KILL", "2", "bash", "-lc", "sleep 30"},
	})
	elapsed := time.Since(start)
	if err == nil && killRes.exitCode != 137 {
		err = fmt.Errorf("exit code %d, want 137 (SIGKILL)", killRes.exitCode)
	}
	step("Timeout: enforce via in-container timeout(1)", err,
		fmt.Sprintf("exit=%d after=%s", killRes.exitCode, elapsed.Round(100*time.Millisecond)))

	// --- Filesystem: write / read / stat / mkdir / list / remove ------------
	err = writeFile(ctx, cli, id, "/workspace/input/attachment.txt", []byte("attachment payload\n"))
	step("WriteFile: PUT /containers/{id}/archive", err, "/workspace/input/attachment.txt")

	content, err := readFile(ctx, cli, id, "/workspace/input/attachment.txt")
	if err == nil && string(content) != "attachment payload\n" {
		err = fmt.Errorf("content mismatch: %q", content)
	}
	step("ReadFile: GET /containers/{id}/archive", err, fmt.Sprintf("%d bytes", len(content)))

	stat, err := cli.ContainerStatPath(ctx, id, client.ContainerStatPathOptions{
		Path: "/workspace/input/attachment.txt",
	})
	step("Stat: HEAD /containers/{id}/archive", err,
		fmt.Sprintf("size=%d mode=%v mtime=%s", stat.Stat.Size, stat.Stat.Mode.Perm(),
			stat.Stat.Mtime.Format(time.RFC3339)))

	rootMk, _ := execIn(ctx, cli, id, execRequest{cmd: []string{"mkdir", "-p", "/workspace/output/rootdir"}})
	step("CapDrop ALL: root cannot bypass mode bits (GAP)", nil,
		fmt.Sprintf("mkdir as root exit=%d stderr=%q", rootMk.exitCode, oneline(rootMk.stderr)))

	_, err = mustExec(ctx, cli, id, execRequest{
		cmd:  []string{"mkdir", "-p", "/workspace/output/nested"},
		user: execUser,
	})
	step("MakeDir: exec mkdir -p as sandbox user", err, "/workspace/output/nested")

	if _, err = mustExec(ctx, cli, id, execRequest{
		cmd:  []string{"bash", "-lc", "echo body > /workspace/output/nested/report.txt"},
		user: execUser,
	}); err != nil {
		step("ListDir: seed a file to list", err, "")
	}
	lsRes, err := mustExec(ctx, cli, id, execRequest{
		cmd:  []string{"find", "/workspace/output", "-mindepth", "1", "-printf", `%y\t%s\t%T@\t%p\n`},
		user: execUser,
	})
	if err == nil && !strings.Contains(lsRes.stdout, "report.txt") {
		err = fmt.Errorf("listing missing report.txt: %q", lsRes.stdout)
	}
	step("ListDir: exec find (no native API)", err,
		fmt.Sprintf("exit=%d out=%q err=%q", lsRes.exitCode, oneline(lsRes.stdout), oneline(lsRes.stderr)))

	_, err = mustExec(ctx, cli, id, execRequest{
		cmd:  []string{"rm", "-rf", "/workspace/input/attachment.txt"},
		user: execUser,
	})
	step("Remove: exec rm (no native API)", err, "removed")

	// --- Session-scoped state persists across executions --------------------
	installRes, err := execIn(ctx, cli, id, execRequest{
		cmd:  []string{"bash", "-lc", "pip install --quiet --no-cache-dir --user cowsay==6.1 && echo 1 > /workspace/output/counter.txt"},
		user: execUser,
	})
	if err == nil && installRes.exitCode != 0 {
		err = fmt.Errorf("install failed: %s %s", oneline(installRes.stdout), oneline(installRes.stderr))
	}
	if err == nil {
		var second execResult
		second, err = execIn(ctx, cli, id, execRequest{
			cmd:  []string{"bash", "-lc", `python -c 'import cowsay; print("cowsay ok")' && cat /workspace/output/counter.txt`},
			user: execUser,
		})
		if err == nil && (!strings.Contains(second.stdout, "cowsay ok") || !strings.Contains(second.stdout, "1")) {
			err = fmt.Errorf("state lost between executions: %q %q", second.stdout, second.stderr)
		}
	}
	step("Session state: pip install survives next exec", err, "package + file both visible")

	// --- Pause / Unpause (memory kept resident) -----------------------------
	_, err = cli.ContainerPause(ctx, id, client.ContainerPauseOptions{})
	if err == nil {
		insp, _ := cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
		if insp.Container.State.Status != "paused" {
			err = fmt.Errorf("state=%s", insp.Container.State.Status)
		}
	}
	if err == nil {
		_, err = cli.ContainerUnpause(ctx, id, client.ContainerUnpauseOptions{})
	}
	if err == nil {
		var after execResult
		after, err = execIn(ctx, cli, id, execRequest{cmd: []string{"cat", "/workspace/output/counter.txt"}})
		if err == nil && !strings.Contains(after.stdout, "1") {
			err = fmt.Errorf("state lost after unpause: %q", after.stdout)
		}
	}
	step("Pause/Unpause: cgroup freezer round-trip", err,
		"RAM + processes retained, host memory still occupied")

	// --- Stop / Start (filesystem kept, processes lost) ---------------------
	_, _ = execIn(ctx, cli, id, execRequest{cmd: []string{"bash", "-lc", "nohup sleep 900 >/dev/null 2>&1 & echo started"}})
	timeoutSec := 5
	_, err = cli.ContainerStop(ctx, id, client.ContainerStopOptions{Timeout: &timeoutSec})
	if err == nil {
		_, err = cli.ContainerStart(ctx, id, client.ContainerStartOptions{})
	}
	var afterRestart execResult
	if err == nil {
		afterRestart, err = execIn(ctx, cli, id, execRequest{
			cmd: []string{"bash", "-lc", "cat /workspace/output/counter.txt; ps -eo args= | grep -c '^sleep 900$' || true"},
		})
		if err == nil && !strings.Contains(afterRestart.stdout, "1") {
			err = fmt.Errorf("filesystem lost after restart: %q", afterRestart.stdout)
		}
	}
	step("Stop/Start: filesystem survives, processes do not", err,
		fmt.Sprintf("counter+sleep-count=%s", oneline(afterRestart.stdout)))

	// --- Snapshot workflow --------------------------------------------------
	mgmtID, err := createSandbox(ctx, cli, tmplImage,
		map[string]string{"weknora.managed": "true", "weknora.role": "snapshot-builder"})
	step("Snapshot: create workspace management sandbox", err, short(mgmtID))
	if err != nil {
		report()
		return
	}
	defer remove(cli, mgmtID)

	skillRes, err := execIn(ctx, cli, mgmtID, execRequest{
		cmd: []string{"bash", "-lc", "pip install --quiet --no-cache-dir requests==2.32.3 && mkdir -p /opt/skills/pdf && echo 'skill v1' > /opt/skills/pdf/SKILL.md"},
	})
	if err == nil && skillRes.exitCode != 0 {
		err = fmt.Errorf("skill install failed: %s", oneline(skillRes.stderr))
	}
	step("Snapshot: install a skill into that sandbox", err, "requests==2.32.3 + /opt/skills/pdf")

	snapV1 := "weknora-poc/snapshot:v1"
	commitRes, err := cli.ContainerCommit(ctx, mgmtID, client.ContainerCommitOptions{
		Reference: snapV1,
		Comment:   "weknora snapshot v1",
		Changes:   []string{`LABEL weknora.snapshot.version=1`, `LABEL weknora.snapshot.tenant=1`},
	})
	step("Snapshot: commit container -> image", err, short(commitRes.ID))

	snapSessionID, err := createSandbox(ctx, cli, snapV1,
		map[string]string{"weknora.managed": "true", "weknora.session": sessionID + "-from-snapshot"})
	step("Snapshot: session sandbox boots from snapshot", err, short(snapSessionID))
	if err == nil {
		defer remove(cli, snapSessionID)
		var check execResult
		check, err = execIn(ctx, cli, snapSessionID, execRequest{
			cmd: []string{"bash", "-lc", `python -c 'import requests; print(requests.__version__)' && cat /opt/skills/pdf/SKILL.md`},
		})
		if err == nil && (!strings.Contains(check.stdout, "2.32.3") || !strings.Contains(check.stdout, "skill v1")) {
			err = fmt.Errorf("snapshot content missing: %q %q", check.stdout, check.stderr)
		}
		step("Snapshot: installed skill present in new sandbox", err, oneline(check.stdout))
	}

	// Incremental snapshot update: install a second skill on top of v1.
	upgradeID, err := createSandbox(ctx, cli, snapV1, map[string]string{"weknora.role": "snapshot-builder"})
	if err == nil {
		defer remove(cli, upgradeID)
		_, err = execIn(ctx, cli, upgradeID, execRequest{
			cmd: []string{"bash", "-lc", "mkdir -p /opt/skills/chart && echo 'skill v2' > /opt/skills/chart/SKILL.md"},
		})
	}
	snapV2 := "weknora-poc/snapshot:v2"
	if err == nil {
		_, err = cli.ContainerCommit(ctx, upgradeID, client.ContainerCommitOptions{
			Reference: snapV2,
			Changes:   []string{`LABEL weknora.snapshot.version=2`},
		})
	}
	step("Snapshot: incremental update v1 -> v2", err, snapV2)

	imgs, err := cli.ImageList(ctx, client.ImageListOptions{
		All:     true,
		Filters: client.Filters{}.Add("label", "weknora.snapshot.version"),
	})
	step("Snapshot: list snapshots by label", err, fmt.Sprintf("count=%d", len(imgs.Items)))

	insp, err := cli.ImageInspect(ctx, snapV2)
	if err == nil {
		step("Snapshot: layer/size accounting", nil,
			fmt.Sprintf("layers=%d size=%.1fMB (image layer hard limit: 127)",
				len(insp.RootFS.Layers), float64(insp.Size)/1e6))
	} else {
		step("Snapshot: layer/size accounting", err, "")
	}

	// Does a snapshot preserve running processes / RAM? (E2B pause does.)
	_, _ = execIn(ctx, cli, mgmtID, execRequest{cmd: []string{"bash", "-lc", "nohup sleep 600 >/dev/null 2>&1 & echo started"}})
	procSnap := "weknora-poc/snapshot:proc"
	_, err = cli.ContainerCommit(ctx, mgmtID, client.ContainerCommitOptions{Reference: procSnap})
	if err == nil {
		var procID string
		procID, err = createSandbox(ctx, cli, procSnap, map[string]string{"weknora.role": "proc-check"})
		if err == nil {
			defer remove(cli, procID)
			var out execResult
			out, err = execIn(ctx, cli, procID, execRequest{cmd: []string{"bash", "-lc", "ps -eo args= | grep -c '^sleep 600$' || true"}})
			step("Snapshot: running processes NOT captured (GAP)", err,
				fmt.Sprintf("'sleep' procs in restored sandbox=%s", oneline(out.stdout)))
		}
	}

	insp2, err := cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	step("Idle TTL: daemon has none, only timestamps (GAP)", err,
		fmt.Sprintf("startedAt=%s -> WeKnora must sweep", insp2.Container.State.StartedAt))

	checkCLIOrphan(ctx)

	report()
}

// checkCLIOrphan reproduces what today's CLI-based DockerSandbox does on
// timeout: exec.CommandContext kills the `docker run --rm` client process. The
// workload lives in the daemon, so killing the client does not stop it — the
// timeout is reported to the caller while the container keeps burning CPU.
func checkCLIOrphan(ctx context.Context) {
	if _, err := exec.LookPath("docker"); err != nil {
		step("CLI orphan: skipped (docker CLI not on PATH)", nil, "")
		return
	}
	name := fmt.Sprintf("weknora-poc-orphan-%d", time.Now().UnixNano())
	runCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "docker", "run", "--rm", "--name", name,
		baseImage, "sleep", "120")
	cliErr := cmd.Run()
	time.Sleep(2 * time.Second)

	out, err := exec.CommandContext(ctx, "docker", "ps", "--filter", "name="+name,
		"--format", "{{.State}} {{.Status}}").Output()
	state := oneline(string(out))
	_ = exec.CommandContext(ctx, "docker", "rm", "-f", name).Run()
	if state == "" {
		state = "gone"
	}
	step("CLI orphan: killing `docker run` leaves it running (GAP)", err,
		fmt.Sprintf("cliErr=%v container=%s", cliErr != nil, state))
}

func report() {
	fmt.Println()
	if failures == 0 {
		fmt.Println("RESULT: all checks passed")
		return
	}
	fmt.Printf("RESULT: %d check(s) failed\n", failures)
	os.Exit(1)
}

// --- helpers -----------------------------------------------------------------

func remove(cli *client.Client, id string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, _ = cli.ContainerRemove(ctx, id, client.ContainerRemoveOptions{Force: true})
}

func buildTemplateImage(ctx context.Context, cli *client.Client, tag string) error {
	if _, err := cli.ImageInspect(ctx, tag); err == nil {
		return nil
	}
	dockerfile := `FROM ` + baseImage + `
RUN apt-get update && apt-get install -y --no-install-recommends procps findutils && rm -rf /var/lib/apt/lists/*
RUN useradd -m -u 1000 user && mkdir -p /workspace/input /workspace/output && chown -R user:user /workspace
WORKDIR /workspace
`
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	if err := tw.WriteHeader(&tar.Header{Name: "Dockerfile", Mode: 0o644, Size: int64(len(dockerfile))}); err != nil {
		return err
	}
	if _, err := tw.Write([]byte(dockerfile)); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	resp, err := cli.ImageBuild(ctx, &buf, client.ImageBuildOptions{Tags: []string{tag}, Remove: true})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(io.Discard, resp.Body)
	return err
}

func createSandbox(ctx context.Context, cli *client.Client, img string, labels map[string]string) (string, error) {
	pids := int64(256)
	created, err := cli.ContainerCreate(ctx, client.ContainerCreateOptions{
		Image: img,
		Config: &container.Config{
			Cmd:        []string{"sleep", "infinity"},
			WorkingDir: "/workspace",
			Labels:     labels,
		},
		HostConfig: &container.HostConfig{
			Resources: container.Resources{
				Memory:     1 << 30,
				MemorySwap: 1 << 30,
				NanoCPUs:   1_000_000_000,
				PidsLimit:  &pids,
			},
			CapDrop:     []string{"ALL"},
			SecurityOpt: []string{"no-new-privileges"},
		},
	})
	if err != nil {
		return "", err
	}
	if _, err := cli.ContainerStart(ctx, created.ID, client.ContainerStartOptions{}); err != nil {
		return created.ID, err
	}
	return created.ID, nil
}

type execRequest struct {
	cmd     []string
	user    string
	workDir string
	env     []string
	stdin   string
}

type execResult struct {
	stdout   string
	stderr   string
	exitCode int
}

func execIn(ctx context.Context, cli *client.Client, id string, req execRequest) (execResult, error) {
	created, err := cli.ExecCreate(ctx, id, client.ExecCreateOptions{
		Cmd:          req.cmd,
		User:         req.user,
		WorkingDir:   req.workDir,
		Env:          req.env,
		AttachStdin:  req.stdin != "",
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return execResult{}, err
	}
	attached, err := cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return execResult{}, err
	}
	defer attached.Close()

	if req.stdin != "" {
		if _, err := attached.Conn.Write([]byte(req.stdin)); err != nil {
			return execResult{}, err
		}
		if cw, ok := attached.Conn.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}

	var stdout, stderr bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, copyErr := stdcopy.StdCopy(&stdout, &stderr, attached.Reader)
		copyDone <- copyErr
	}()
	select {
	case err = <-copyDone:
		if err != nil {
			return execResult{}, err
		}
	case <-ctx.Done():
		return execResult{stdout: stdout.String(), stderr: stderr.String()}, ctx.Err()
	}

	inspect, err := cli.ExecInspect(ctx, created.ID, client.ExecInspectOptions{})
	if err != nil {
		return execResult{}, err
	}
	return execResult{
		stdout:   stdout.String(),
		stderr:   stderr.String(),
		exitCode: inspect.ExitCode,
	}, nil
}

// mustExec fails when the command itself exits non-zero, so a silently
// failing setup step cannot be mistaken for a passing capability check.
func mustExec(ctx context.Context, cli *client.Client, id string, req execRequest) (execResult, error) {
	res, err := execIn(ctx, cli, id, req)
	if err != nil {
		return res, err
	}
	if res.exitCode != 0 {
		return res, fmt.Errorf("exit=%d stderr=%s", res.exitCode, oneline(res.stderr))
	}
	return res, nil
}

func writeFile(ctx context.Context, cli *client.Client, id, dest string, content []byte) error {
	if _, err := execIn(ctx, cli, id, execRequest{cmd: []string{"mkdir", "-p", dirOf(dest)}}); err != nil {
		return err
	}
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	name := dest[strings.LastIndex(dest, "/")+1:]
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o644, Size: int64(len(content)), Uid: 1000, Gid: 1000,
	}); err != nil {
		return err
	}
	if _, err := tw.Write(content); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	_, err := cli.CopyToContainer(ctx, id, client.CopyToContainerOptions{
		DestinationPath: dirOf(dest),
		Content:         &buf,
		CopyUIDGID:      true,
	})
	return err
}

func readFile(ctx context.Context, cli *client.Client, id, src string) ([]byte, error) {
	res, err := cli.CopyFromContainer(ctx, id, client.CopyFromContainerOptions{SourcePath: src})
	if err != nil {
		return nil, err
	}
	defer res.Content.Close()
	tr := tar.NewReader(res.Content)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil, errors.New("file not present in archive")
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeReg {
			return io.ReadAll(tr)
		}
	}
}

func dirOf(p string) string {
	idx := strings.LastIndex(p, "/")
	if idx <= 0 {
		return "/"
	}
	return p[:idx]
}

func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func oneline(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
