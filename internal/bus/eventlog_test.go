package bus

import (
	"context"
	"testing"
	"time"
)

func seqs(es []LogEntry) []uint64 {
	out := make([]uint64, 0, len(es))
	for _, e := range es {
		out = append(out, e.Seq)
	}
	return out
}

func TestMemoryEventLog_ReadIsRepeatable(t *testing.T) {
	l := newMemoryEventLog(0, 0)
	for _, subj := range []string{"f.a.x", "f.b.x", "f.a.y"} {
		l.Append(subj, []byte(subj))
	}
	ctx := context.Background()
	first, err := l.ReadLog(ctx, "f.a.>", 0, 10, 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got := seqs(first); len(got) != 2 || got[0] != 1 || got[1] != 3 {
		t.Fatalf("first read seqs = %v, want [1 3]", got)
	}
	// Reading does not consume: the same position yields the same entries.
	again, _ := l.ReadLog(ctx, "f.a.>", 0, 10, 100*time.Millisecond)
	if len(again) != 2 {
		t.Fatalf("re-read got %d entries, want 2", len(again))
	}
	// After the last one there is nothing.
	rest, _ := l.ReadLog(ctx, "f.a.>", 3, 10, 50*time.Millisecond)
	if len(rest) != 0 {
		t.Fatalf("read after last got %d entries", len(rest))
	}
}

func TestMemoryEventLog_WaitsForAppend(t *testing.T) {
	l := newMemoryEventLog(0, 0)
	go func() {
		time.Sleep(50 * time.Millisecond)
		l.Append("f.a.x", []byte("x"))
	}()
	got, err := l.ReadLog(context.Background(), "f.a.>", 0, 10, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
}

func TestMemoryEventLog_MaxAndRetention(t *testing.T) {
	l := newMemoryEventLog(3, time.Hour)
	for i := 0; i < 5; i++ {
		l.Append("f.a.x", []byte{byte(i)})
	}
	st, _ := l.LogState(context.Background())
	if st.FirstSeq != 3 || st.LastSeq != 5 {
		t.Fatalf("state = %+v, want first 3 last 5", st)
	}
	got, _ := l.ReadLog(context.Background(), "f.>", 0, 2, 50*time.Millisecond)
	if s := seqs(got); len(s) != 2 || s[0] != 3 || s[1] != 4 {
		t.Fatalf("seqs = %v, want [3 4]", s)
	}

	now := time.Now()
	l.clock = func() time.Time { return now.Add(2 * time.Hour) }
	st, _ = l.LogState(context.Background())
	if st.FirstSeq != 0 || st.LastSeq != 5 {
		t.Fatalf("after expiry state = %+v, want empty with last 5", st)
	}
}

func TestMemoryEventLog_EpochPerInstance(t *testing.T) {
	a, _ := newMemoryEventLog(0, 0).LogState(context.Background())
	b, _ := newMemoryEventLog(0, 0).LogState(context.Background())
	if a.Epoch == "" || a.Epoch == b.Epoch {
		t.Fatalf("epochs %q and %q must be set and differ", a.Epoch, b.Epoch)
	}
}

func TestNewMemoryEventLog_FedByBus(t *testing.T) {
	b := NewChannel()
	l, err := NewMemoryEventLog(b, "f.*.>", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	_ = b.Publish(context.Background(), "f.a.x", []byte("x"))
	_ = b.Publish(context.Background(), "other.a.x", []byte("y"))
	got, _ := l.ReadLog(context.Background(), "f.a.>", 0, 10, time.Second)
	if len(got) != 1 || string(got[0].Data) != "x" {
		t.Fatalf("got %+v, want the one f.a.x entry", got)
	}
}

func TestAsEventLog_UnwrapsInstrumented(t *testing.T) {
	inner := &logBus{Bus: NewChannel()}
	wrapped := &InstrumentedBus{Bus: inner}
	if _, ok := AsEventLog(wrapped); !ok {
		t.Fatal("AsEventLog did not find the wrapped bus's log")
	}
	if _, ok := AsEventLog(NewChannel()); ok {
		t.Fatal("ChannelBus must not report a log")
	}
}

type logBus struct {
	Bus
	*MemoryEventLog
}

func TestMemoryEventLog_SeekAfter(t *testing.T) {
	l := newMemoryEventLog(0, 0)
	now := time.Now()
	l.clock = func() time.Time { return now }
	for i, subj := range []string{"f.a.x", "f.b.x", "f.a.y", "f.a.z"} {
		now = now.Add(time.Duration(i) * time.Second)
		l.Append(subj, []byte(subj))
	}
	// Times: t0, t0+1s (seq 2), t0+3s (seq 3), t0+6s (seq 4).
	t0 := now.Add(-6 * time.Second)
	is := func(want string) func([]byte) bool {
		return func(d []byte) bool { return string(d) == want }
	}
	if got := l.SeekAfter("f.a.>", t0, is("f.a.y")); got != 3 {
		t.Fatalf("anchor found: seq %d, want 3", got)
	}
	// An anchor on another subject is not a match.
	if got := l.SeekAfter("f.a.>", t0, is("f.b.x")); got != 0 {
		t.Fatalf("anchor on other subject: seq %d, want 0", got)
	}
	// No anchor: resume before the first entry at or after notBefore.
	if got := l.SeekAfter("f.a.>", t0.Add(2*time.Second), nil); got != 2 {
		t.Fatalf("time fallback: seq %d, want 2", got)
	}
	// An anchor older than the window is not searched for.
	if got := l.SeekAfter("f.a.>", t0.Add(2*time.Second), is("f.a.x")); got != 2 {
		t.Fatalf("anchor before window: seq %d, want 2", got)
	}
	if got := l.SeekAfter("f.a.>", now.Add(time.Hour), nil); got != 4 {
		t.Fatalf("window after every entry: seq %d, want 4", got)
	}
}
