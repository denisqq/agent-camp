// Package runner owns a bounded pool of trusted Python adapters and Monty workers.
package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"github.com/denisqq/agent-camp/internal/camp"
	"io"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
)

type Config struct {
	Python, Script, Monty string
	Workers               int
}
type process struct {
	cmd *exec.Cmd
	in  io.WriteCloser
	out *bufio.Scanner
}
type Pool struct {
	config Config
	slots  chan struct{}
	idle   chan *process
	mu     sync.Mutex
	closed bool
}

func New(c Config) *Pool {
	if c.Workers < 1 {
		c.Workers = 2
	}
	return &Pool{config: c, slots: make(chan struct{}, c.Workers), idle: make(chan *process, c.Workers)}
}
func (p *Pool) spawn() (*process, error) {
	c := p.config
	if !filepath.IsAbs(c.Python) || !filepath.IsAbs(c.Script) || !filepath.IsAbs(c.Monty) {
		return nil, errors.New("runner paths must be absolute")
	}
	cmd := exec.Command(c.Python, "-I", c.Script, "--monty", c.Monty)
	cmd.Env = []string{"PATH=" + filepath.Dir(c.Python) + ":/usr/bin:/bin", "PYTHONUNBUFFERED=1"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	cmd.Stderr = io.Discard
	if err = cmd.Start(); err != nil {
		return nil, err
	}
	scan := bufio.NewScanner(out)
	scan.Buffer(make([]byte, 4096), 2*1024*1024)
	return &process{cmd, in, scan}, nil
}
func stop(proc *process) {
	_ = syscall.Kill(-proc.cmd.Process.Pid, syscall.SIGKILL)
	_ = proc.in.Close()
	_ = proc.cmd.Wait()
}
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	for {
		select {
		case proc := <-p.idle:
			stop(proc)
		default:
			p.mu.Unlock()
			return
		}
	}
}
func (p *Pool) Execute(ctx context.Context, code string, callback camp.Callback) (camp.RunOutput, error) {
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return camp.RunOutput{}, ctx.Err()
	}
	defer func() { <-p.slots }()
	p.mu.Lock()
	closed := p.closed
	p.mu.Unlock()
	if closed {
		return camp.RunOutput{}, errors.New("runner closed")
	}
	var proc *process
	select {
	case proc = <-p.idle:
	default:
		var err error
		proc, err = p.spawn()
		if err != nil {
			return camp.RunOutput{}, err
		}
	}
	healthy := false
	defer func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		if healthy && !p.closed {
			p.idle <- proc
		} else {
			stop(proc)
		}
	}()
	type outcome struct {
		value camp.RunOutput
		err   error
	}
	done := make(chan outcome, 1)
	go func() { out, err := exchange(ctx, proc, code, callback); done <- outcome{out, err} }()
	select {
	case result := <-done:
		healthy = result.err == nil
		return result.value, result.err
	case <-ctx.Done():
		_ = syscall.Kill(-proc.cmd.Process.Pid, syscall.SIGKILL)
		result := <-done
		_ = result
		return camp.RunOutput{}, ctx.Err()
	}
}
func exchange(ctx context.Context, p *process, code string, callback camp.Callback) (camp.RunOutput, error) {
	encoder := json.NewEncoder(p.in)
	if err := encoder.Encode(map[string]any{"code": code}); err != nil {
		return camp.RunOutput{}, err
	}
	for p.out.Scan() {
		var msg struct {
			Type   string         `json:"type"`
			Method string         `json:"method"`
			Args   map[string]any `json:"args"`
			Result any            `json:"result"`
			Stdout string         `json:"stdout"`
			Error  string         `json:"error"`
		}
		if err := json.Unmarshal(p.out.Bytes(), &msg); err != nil {
			return camp.RunOutput{}, err
		}
		switch msg.Type {
		case "call":
			if err := ctx.Err(); err != nil {
				return camp.RunOutput{}, err
			}
			value, err := callback(ctx, msg.Method, msg.Args)
			reply := map[string]any{"result": value}
			if err != nil {
				reply = map[string]any{"error": err.Error()}
			}
			if err = encoder.Encode(reply); err != nil {
				return camp.RunOutput{}, err
			}
		case "result":
			return camp.RunOutput{Value: msg.Result, Stdout: msg.Stdout}, nil
		case "error":
			return camp.RunOutput{}, errors.New(msg.Error)
		default:
			return camp.RunOutput{}, errors.New("invalid runner frame")
		}
	}
	if err := p.out.Err(); err != nil {
		return camp.RunOutput{}, err
	}
	return camp.RunOutput{}, errors.New("runner stopped")
}
