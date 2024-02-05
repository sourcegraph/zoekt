package main

import (
	"bytes"
	"context"
	"errors"
	"log"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

var cmdTimeout = 100 * time.Millisecond

func captureOutput(f func()) string {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer func() { log.SetOutput(os.Stderr) }()
	f()
	return buf.String()
}

func TestLoggedRun(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "echo", "-n", "1")

	stdout := captureOutput(func() {
		loggedRun(cmd)
	})

	if !strings.Contains(stdout, "run [echo -n 1]") {
		t.Errorf("loggedRun output is incorrect: %v", stdout)
	}
}

func TestLoggedRunFailure(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), cmdTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "false")

	stdout := captureOutput(func() {
		loggedRun(cmd)
	})

	if !strings.Contains(stdout, "failed: exit status 1") {
		t.Errorf("loggedRun output is incorrect: %v", stdout)
	}
}

func TestInitMetrics(t *testing.T) {
	server := indexServer{}

	server.initMetrics()

	if server.promRegistry == nil {
		t.Errorf("promRegistry shouldn't be nil")
	}

	if server.metricsRequestsTotal == nil {
		t.Errorf("metricsRequestsTotal shouldn't be nil")
	}
}

func TestIndexRepository(t *testing.T) {
	var cmdHistory [][]string

	executeCmd = func(ctx context.Context, name string, arg ...string) (err error) {
		currentCmd := append([]string{name}, arg...)
		cmdHistory = append(cmdHistory, currentCmd)

		return
	}

	opts := Options{
		indexTimeout: cmdTimeout,
		repoDir:      "/repo_dir",
		indexDir:     "/index_dir",
	}

	req := indexRequest{
		CloneURL: "https://example.com/repository.git",
		RepoID:   100,
	}

	_, err := indexRepository(opts, req)
	if err != nil {
		t.Fatal(err)
	}

	expectedHistory := [][]string{
		{"zoekt-git-clone", "-dest", "/repo_dir", "-name", "100", "-repoid", "100", "https://example.com/repository.git"},
		{"git", "-C", "/repo_dir/100.git", "fetch"},
		{"zoekt-git-index", "-index", "/index_dir", "/repo_dir/100.git"},
	}

	if !reflect.DeepEqual(cmdHistory, expectedHistory) {
		t.Errorf("cmdHistory output is incorrect: %v, expected output: %v", cmdHistory, expectedHistory)
	}
}

func TestIndexRepositoryWhenErr(t *testing.T) {
	var cmdHistory [][]string

	executeCmd = func(ctx context.Context, name string, arg ...string) (err error) {
		currentCmd := append([]string{name}, arg...)
		cmdHistory = append(cmdHistory, currentCmd)

		if len(cmdHistory) > 1 {
			return errors.New("command failed")
		}

		return
	}

	opts := Options{
		indexTimeout: cmdTimeout,
		repoDir:      "/repo_dir",
		indexDir:     "/index_dir",
	}

	req := indexRequest{
		CloneURL: "https://example.com/repository.git",
		RepoID:   100,
	}

	_, err := indexRepository(opts, req)

	if err == nil {
		t.Errorf("Error is empty, when it should be present")
	}

	expectedHistory := [][]string{
		{"zoekt-git-clone", "-dest", "/repo_dir", "-name", "100", "-repoid", "100", "https://example.com/repository.git"},
		{"git", "-C", "/repo_dir/100.git", "fetch"},
	}

	if !reflect.DeepEqual(cmdHistory, expectedHistory) {
		t.Errorf("cmdHistory output is incorrect: %v, expected output: %v", cmdHistory, expectedHistory)
	}
}
