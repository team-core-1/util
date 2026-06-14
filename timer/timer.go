package tmr

import (
	"errors"
	"time"

	"github.com/team-core-1/util/queue"

	"github.com/RussellLuo/timingwheel"
)

var (
	ErrNil = errors.New("Tmr fail(nil)")
)

type TmrEngine[T any] struct {
	tw *timingwheel.TimingWheel
	q  *queue.Queue[T]
}

type Tmr struct {
	t *timingwheel.Timer
}

func New[T any](tw *timingwheel.TimingWheel, capacity int) (*TmrEngine[T], error) {
	q, err := queue.New[T](capacity)
	if err != nil {
		return nil, ErrNil
	}

	t := &TmrEngine[T]{
		tw: tw,
		q:  q,
	}

	return t, nil
}

func (te *TmrEngine[T]) C() <-chan T {
	return te.q.C()
}

func (te *TmrEngine[T]) Set(d time.Duration, key T) *Tmr {
	f := func() {
		te.q.Enqueue(key)
	}

	return &Tmr{
		t: te.tw.AfterFunc(d, f),
	}
}

func (tmr *Tmr) Cancel() {
	tmr.t.Stop()
}
