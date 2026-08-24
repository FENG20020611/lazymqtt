// Command mqttload is a development-only MQTT load generator.
//
// It exists because a `mosquitto_pub` loop cannot give you a precise rate, a
// controlled topic cardinality or a reproducible payload size, and those three
// knobs are exactly what validate lazymqtt's coalescer and memory ceiling.
//
//	mqttload --rate 10000 --topics 500 --payload 256 --duration 60s
//	mqttload --pattern sawtooth --rate 5000       # bursty, exercises batching
//	mqttload --pattern retained --count 20000     # retained flood
//
// It is excluded from release builds.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"
)

func main() { os.Exit(run(os.Args[1:])) }

type options struct {
	broker   string
	clientID string
	username string
	password string
	prefix   string

	rate     float64
	topics   int
	payload  int
	qos      uint
	retain   bool
	duration time.Duration
	count    int
	pattern  string
	period   time.Duration
	workers  int
	quiet    bool
}

func run(args []string) int {
	var o options
	fs := flag.NewFlagSet("mqttload", flag.ContinueOnError)
	fs.StringVar(&o.broker, "broker", "tcp://localhost:1883", "broker URL")
	fs.StringVar(&o.clientID, "client-id", "mqttload", "client id")
	fs.StringVar(&o.username, "username", "", "username")
	fs.StringVar(&o.password, "password", "", "password")
	fs.StringVar(&o.prefix, "prefix", "load", "topic prefix")
	fs.Float64Var(&o.rate, "rate", 1000, "messages per second")
	fs.IntVar(&o.topics, "topics", 100, "distinct topics to rotate through")
	fs.IntVar(&o.payload, "payload", 256, "payload size in bytes")
	fs.UintVar(&o.qos, "qos", 0, "QoS 0, 1 or 2")
	fs.BoolVar(&o.retain, "retain", false, "set the retain flag")
	fs.DurationVar(&o.duration, "duration", 30*time.Second, "how long to run (0 = until interrupted)")
	fs.IntVar(&o.count, "count", 20000, "message count for --pattern retained")
	fs.StringVar(&o.pattern, "pattern", "steady", "steady | sawtooth | retained")
	fs.DurationVar(&o.period, "period", 5*time.Second, "sawtooth ramp period")
	fs.IntVar(&o.workers, "workers", 8, "concurrent publishers (matters for QoS 1 and 2)")
	fs.BoolVar(&o.quiet, "quiet", false, "suppress the per-second progress line")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	pattern, err := ParsePattern(o.pattern)
	if err != nil {
		fmt.Fprintln(os.Stderr, "mqttload:", err)
		return 2
	}
	if o.qos > 2 {
		fmt.Fprintln(os.Stderr, "mqttload: qos must be 0, 1 or 2")
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := generate(ctx, o, pattern); err != nil {
		fmt.Fprintln(os.Stderr, "mqttload:", err)
		return 1
	}
	return 0
}

// counters is the shared tally. Reported once a second and again at the end.
type counters struct {
	sent    atomic.Uint64
	errs    atomic.Uint64
	skipped atomic.Uint64 // scheduled but dropped because every worker was busy
}

func generate(ctx context.Context, o options, pattern Pattern) error {
	u, err := url.Parse(o.broker)
	if err != nil {
		return fmt.Errorf("invalid broker url %q: %w", o.broker, err)
	}

	cm, err := connect(ctx, u, o)
	if err != nil {
		return err
	}
	defer func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = cm.Disconnect(shutdown)
	}()

	if err := cm.AwaitConnection(ctx); err != nil {
		return fmt.Errorf("waiting for a connection: %w", err)
	}

	topics := NewTopicSet(o.prefix, o.topics)
	var c counters
	start := time.Now()

	// jobs is deliberately shallow. A deep queue would let the generator
	// report a rate it never actually achieved; a shallow one makes falling
	// behind visible in the skipped counter instead.
	jobs := make(chan job, max(o.workers*4, 32))
	var wg sync.WaitGroup
	for i := 0; i < max(o.workers, 1); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			publisher(ctx, cm, jobs, &c)
		}()
	}

	runCtx := ctx
	var cancel context.CancelFunc
	if pattern != PatternRetained && o.duration > 0 {
		runCtx, cancel = context.WithTimeout(ctx, o.duration)
		defer cancel()
	}

	reportDone := make(chan struct{})
	if !o.quiet {
		go report(runCtx, &c, start, reportDone)
	} else {
		close(reportDone)
	}

	if pattern == PatternRetained {
		floodRetained(runCtx, o, topics, jobs, &c)
	} else {
		steady(runCtx, o, pattern, topics, jobs, &c)
	}

	close(jobs)
	wg.Wait()
	<-reportDone

	elapsed := time.Since(start)
	sent, errs, skipped := c.sent.Load(), c.errs.Load(), c.skipped.Load()
	fmt.Printf("\nsent %d in %s (%.0f msg/s), %d errors, %d skipped, %d topics, %d-byte payloads\n",
		sent, elapsed.Round(time.Millisecond), float64(sent)/elapsed.Seconds(),
		errs, skipped, topics.Len(), o.payload)
	if errs > 0 {
		return fmt.Errorf("%d publishes failed", errs)
	}
	return nil
}

func connect(ctx context.Context, u *url.URL, o options) (*autopaho.ConnectionManager, error) {
	cfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{u},
		KeepAlive:                     30,
		CleanStartOnInitialConnection: true,
		ConnectTimeout:                10 * time.Second,
		ConnectUsername:               o.username,
		ConnectPassword:               []byte(o.password),
		ClientConfig: paho.ClientConfig{
			ClientID: fmt.Sprintf("%s-%d", o.clientID, os.Getpid()),
		},
	}
	cm, err := autopaho.NewConnection(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", u, err)
	}
	return cm, nil
}

type job struct {
	topic   string
	payload []byte
	qos     byte
	retain  bool
}

func publisher(ctx context.Context, cm *autopaho.ConnectionManager, jobs <-chan job, c *counters) {
	for j := range jobs {
		p := &paho.Publish{Topic: j.topic, Payload: j.payload, QoS: j.qos, Retain: j.retain}
		if j.qos == 0 {
			// QoS 0 has nothing to wait for, and the queueing variant keeps
			// the write off the critical path.
			if err := cm.PublishViaQueue(ctx, &autopaho.QueuePublish{Publish: p}); err != nil {
				c.errs.Add(1)
				continue
			}
			c.sent.Add(1)
			continue
		}
		if _, err := cm.Publish(ctx, p); err != nil {
			c.errs.Add(1)
			continue
		}
		c.sent.Add(1)
	}
}

// steady drives the scheduler, offering a batch of messages per tick.
func steady(ctx context.Context, o options, pattern Pattern, topics *TopicSet, jobs chan<- job, c *counters) {
	sched := NewScheduler(o.rate, 10*time.Millisecond, o.period, pattern)
	ticker := time.NewTicker(sched.Interval())
	defer ticker.Stop()

	var seq uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for n := sched.Next(); n > 0; n-- {
				seq++
				offer(ctx, jobs, job{
					topic:   topics.Next(),
					payload: BuildPayload(seq, o.payload),
					qos:     byte(o.qos),
					retain:  o.retain,
				}, c)
			}
		}
	}
}

// floodRetained publishes count retained messages as fast as the broker will
// take them, one per topic, then returns.
func floodRetained(ctx context.Context, o options, topics *TopicSet, jobs chan<- job, c *counters) {
	// A retained flood wants one message per topic, so the topic set is the
	// count rather than the rotation length.
	set := NewTopicSet(o.prefix, o.count)
	for i := 0; i < o.count; i++ {
		if ctx.Err() != nil {
			return
		}
		offer(ctx, jobs, job{
			topic:   set.At(i),
			payload: BuildPayload(uint64(i+1), o.payload),
			qos:     byte(o.qos),
			retain:  true,
		}, c)
	}
	_ = topics
}

// offer hands a job to a worker, or counts it as skipped when they are all
// busy. Blocking here would silently turn a rate limit into a rate ceiling.
func offer(ctx context.Context, jobs chan<- job, j job, c *counters) {
	select {
	case jobs <- j:
	case <-ctx.Done():
	default:
		c.skipped.Add(1)
	}
}

func report(ctx context.Context, c *counters, start time.Time, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var last uint64
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sent := c.sent.Load()
			fmt.Printf("\r%6s  sent %-10d  %6d msg/s  errors %-6d skipped %-8d",
				time.Since(start).Round(time.Second), sent, sent-last,
				c.errs.Load(), c.skipped.Load())
			last = sent
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
