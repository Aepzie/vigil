package collector

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Aepzie/vigil/internal/metrics"
	"github.com/Aepzie/vigil/internal/sshclient"
	"github.com/Aepzie/vigil/internal/store"
)

// poller polls one server on its own goroutine. Everything below the
// marked line is owned by that goroutine and needs no locking.
type poller struct {
	hub     *Hub
	entry   *entry
	id      string
	srv     store.Server
	ctx     context.Context
	cancel  context.CancelFunc
	after   <-chan struct{}
	done    chan struct{}
	trigger chan struct{}

	client   *ssh.Client
	prev     *sample
	lastSeen time.Time
	ident    identity
	backoff  backoff
	nextDial time.Time
	lastErr  error
}

// identity is the static host information kept while a server is down.
type identity struct {
	hostname, os, kernel string
	cpuCount             int
}

func newPoller(ctx context.Context, h *Hub, e *entry, srv store.Server, after <-chan struct{}) *poller {
	pctx, cancel := context.WithCancel(ctx)
	return &poller{
		hub:     h,
		entry:   e,
		id:      srv.ID,
		srv:     srv,
		ctx:     pctx,
		cancel:  cancel,
		after:   after,
		done:    make(chan struct{}),
		trigger: make(chan struct{}, 1),
		backoff: newBackoff(backoffMin, backoffMax),
	}
}

// poke requests an immediate poll without blocking.
func (p *poller) poke() {
	select {
	case p.trigger <- struct{}{}:
	default:
	}
}

// run polls at the hub's interval until the poller's context ends. Polls
// are scheduled from their start time so the history step stays regular;
// a poll slower than the interval is followed immediately by the next.
func (p *poller) run() {
	defer close(p.done)
	defer p.closeClient()
	if p.after != nil {
		select {
		case <-p.after:
		case <-p.ctx.Done():
			return
		}
	}
	timer := time.NewTimer(0)
	defer timer.Stop()
	var lastStart time.Time
	for {
		interval, wake := p.hub.timing()
		forced := false
		select {
		case <-p.ctx.Done():
			return
		case <-wake:
			interval, _ = p.hub.timing()
			timer.Reset(time.Until(lastStart.Add(interval)))
			continue
		case <-p.trigger:
			forced = true
		case <-timer.C:
		}
		lastStart = time.Now()
		p.poll(forced)
		interval, _ = p.hub.timing()
		timer.Reset(time.Until(lastStart.Add(interval)))
	}
}

// poll performs one poll and publishes the result. While the server is
// unreachable it publishes an offline snapshot and only redials when the
// backoff allows it (or when forced by PollNow).
func (p *poller) poll(forced bool) {
	snap, err := p.collect(forced)
	if p.ctx.Err() != nil {
		return
	}
	if err != nil {
		snap = p.offline(err)
	}
	p.hub.publish(p.entry, snap)
}

func (p *poller) collect(forced bool) (metrics.Snapshot, error) {
	if p.client == nil {
		if !forced && time.Now().Before(p.nextDial) {
			return metrics.Snapshot{}, p.lastErr
		}
		if err := p.connect(); err != nil {
			return metrics.Snapshot{}, err
		}
	}
	client := p.client
	ctx, cancel := context.WithTimeout(p.ctx, p.hub.pollTimeout())
	defer cancel()
	stop := context.AfterFunc(ctx, func() { client.Close() })
	nonce := newNonce()
	latency, out, err := exchange(client, nonce)
	if !stop() {
		err = ctx.Err()
	}
	if err != nil {
		p.disconnect(err)
		return metrics.Snapshot{}, err
	}
	cur, err := parseSample(out.text, nonce, time.Now())
	if err != nil {
		return metrics.Snapshot{}, err
	}
	cur.procsTruncated = out.procsTruncated
	snap := buildSnapshot(p.id, cur, p.prev, latency)
	p.prev = cur
	p.lastSeen = snap.At
	p.ident = identity{hostname: snap.Hostname, os: snap.OS, kernel: snap.Kernel, cpuCount: snap.CPUCount}
	return snap, nil
}

func (p *poller) connect() error {
	ctx, cancel := context.WithTimeout(p.ctx, dialTimeout)
	defer cancel()
	client, err := p.hub.dial(ctx, p.srv)
	if err != nil {
		p.scheduleRetry(err)
		return err
	}
	p.client = client
	p.backoff.reset()
	p.lastErr = nil
	return nil
}

func (p *poller) disconnect(err error) {
	p.closeClient()
	p.scheduleRetry(err)
}

func (p *poller) scheduleRetry(err error) {
	p.lastErr = err
	p.nextDial = time.Now().Add(p.backoff.next())
}

func (p *poller) closeClient() {
	if p.client != nil {
		p.client.Close()
		p.client = nil
	}
}

func (p *poller) offline(err error) metrics.Snapshot {
	return metrics.Snapshot{
		ServerID: p.id,
		State:    metrics.StateOffline,
		Err:      reason(err),
		At:       time.Now(),
		LastSeen: p.lastSeen,
		Hostname: p.ident.hostname,
		OS:       p.ident.os,
		Kernel:   p.ident.kernel,
		CPUCount: p.ident.cpuCount,
	}
}

// exchange measures the round trip of an OpenSSH keepalive request and
// then runs pollScript(nonce). Blocking calls are interrupted by closing
// the client.
func exchange(client *ssh.Client, nonce string) (time.Duration, pollOutput, error) {
	start := time.Now()
	if _, _, err := client.SendRequest("keepalive@openssh.com", true, nil); err != nil {
		return 0, pollOutput{}, fmt.Errorf("keepalive: %w", err)
	}
	latency := time.Since(start)
	out, err := runScript(client, nonce)
	return latency, out, err
}

// runScript executes pollScript(nonce) with remoteCommand in a new
// session and reads its stdout with the bounds of readOutput. The
// script's exit status is irrelevant; completeness is checked by the
// parser.
func runScript(client *ssh.Client, nonce string) (pollOutput, error) {
	sess, err := client.NewSession()
	if err != nil {
		return pollOutput{}, fmt.Errorf("open session: %w", err)
	}
	defer sess.Close()
	sess.Stdin = strings.NewReader(pollScript(nonce))
	stdout, err := sess.StdoutPipe()
	if err != nil {
		return pollOutput{}, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := sess.Start(remoteCommand); err != nil {
		return pollOutput{}, fmt.Errorf("exec: %w", err)
	}
	out, err := readOutput(stdout, nonce)
	if err != nil {
		return pollOutput{}, fmt.Errorf("read output: %w", err)
	}
	var exitErr *ssh.ExitError
	var missing *ssh.ExitMissingError
	if err := sess.Wait(); err != nil && !errors.As(err, &exitErr) && !errors.As(err, &missing) {
		return pollOutput{}, fmt.Errorf("exec: %w", err)
	}
	return out, nil
}

// reason renders err as the short Snapshot.Err text.
func reason(err error) string {
	switch {
	case errors.Is(err, errNoProc):
		return errNoProc.Error()
	case errors.Is(err, errIncomplete):
		return errIncomplete.Error()
	case errors.Is(err, errOutputTooLarge):
		return "output too large"
	default:
		return sshclient.Reason(err)
	}
}
