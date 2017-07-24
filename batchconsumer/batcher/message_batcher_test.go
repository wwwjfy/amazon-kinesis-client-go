package batcher

import (
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type batch [][]byte

type MockSync struct {
	flushChan chan struct{}
	batches   []batch
}

func NewMockSync() *MockSync {
	return &MockSync{
		flushChan: make(chan struct{}, 1),
		batches:   []batch{},
	}
}

func (m *MockSync) SendBatch(b [][]byte) {
	m.batches = append(m.batches, batch(b))
	m.flushChan <- struct{}{}
}

func (m *MockSync) waitForFlush(timeout time.Duration) error {
	select {
	case <-m.flushChan:
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("timed out before flush (waited %s)", timeout.String())
	}
}

var mockSequence = SequencePair{big.NewInt(99999), 12345}

func TestBatchingByCount(t *testing.T) {
	assert := assert.New(t)

	sync := NewMockSync()
	batcher, err := New(sync, time.Hour, 2, 1024*1024)
	assert.NoError(err)

	t.Log("Batcher respect count limit")
	assert.NoError(batcher.AddMessage([]byte("hihi"), mockSequence))
	assert.NoError(batcher.AddMessage([]byte("heyhey"), mockSequence))
	assert.NoError(batcher.AddMessage([]byte("hmmhmm"), mockSequence))

	err = sync.waitForFlush(time.Millisecond * 10)
	assert.NoError(err)

	assert.Equal(1, len(sync.batches))
	assert.Equal(2, len(sync.batches[0]))
	assert.Equal("hihi", string(sync.batches[0][0]))
	assert.Equal("heyhey", string(sync.batches[0][1]))

	t.Log("Batcher doesn't send partial batches")
	err = sync.waitForFlush(time.Millisecond * 10)
	assert.Error(err)
}

func TestBatchingByTime(t *testing.T) {
	assert := assert.New(t)

	sync := NewMockSync()
	batcher, err := New(sync, time.Millisecond, 2000000, 1024*1024)
	assert.NoError(err)

	t.Log("Batcher sends partial batches when time expires")
	assert.NoError(batcher.AddMessage([]byte("hihi"), mockSequence))

	err = sync.waitForFlush(time.Millisecond * 10)
	assert.NoError(err)

	assert.Equal(1, len(sync.batches))
	assert.Equal(1, len(sync.batches[0]))
	assert.Equal("hihi", string(sync.batches[0][0]))

	t.Log("Batcher sends all messsages in partial batches when time expires")
	assert.NoError(batcher.AddMessage([]byte("heyhey"), mockSequence))
	assert.NoError(batcher.AddMessage([]byte("yoyo"), mockSequence))

	err = sync.waitForFlush(time.Millisecond * 10)
	assert.NoError(err)

	assert.Equal(2, len(sync.batches))
	assert.Equal(2, len(sync.batches[1]))
	assert.Equal("heyhey", string(sync.batches[1][0]))
	assert.Equal("yoyo", string(sync.batches[1][1]))

	t.Log("Batcher doesn't send empty batches")
	err = sync.waitForFlush(time.Millisecond * 10)
	assert.Error(err)
}

func TestBatchingBySize(t *testing.T) {
	assert := assert.New(t)

	sync := NewMockSync()
	batcher, err := New(sync, time.Hour, 2000000, 8)
	assert.NoError(err)

	t.Log("Large messages are sent immediately")
	assert.NoError(batcher.AddMessage([]byte("hellohello"), mockSequence))

	err = sync.waitForFlush(time.Millisecond * 10)
	assert.NoError(err)

	assert.Equal(1, len(sync.batches))
	assert.Equal(1, len(sync.batches[0]))
	assert.Equal("hellohello", string(sync.batches[0][0]))

	t.Log("Batcher tries not to exceed size limit")
	assert.NoError(batcher.AddMessage([]byte("heyhey"), mockSequence))
	assert.NoError(batcher.AddMessage([]byte("hihi"), mockSequence))

	err = sync.waitForFlush(time.Millisecond * 10)
	assert.NoError(err)

	assert.Equal(2, len(sync.batches))
	assert.Equal(1, len(sync.batches[1]))
	assert.Equal("heyhey", string(sync.batches[1][0]))

	t.Log("Batcher sends messages that didn't fit in previous batch")
	assert.NoError(batcher.AddMessage([]byte("yoyo"), mockSequence)) // At this point "hihi" is in the batch

	err = sync.waitForFlush(time.Millisecond * 10)
	assert.NoError(err)

	assert.Equal(3, len(sync.batches))
	assert.Equal(2, len(sync.batches[2]))
	assert.Equal("hihi", string(sync.batches[2][0]))
	assert.Equal("yoyo", string(sync.batches[2][1]))

	t.Log("Batcher doesn't send partial batches")
	assert.NoError(batcher.AddMessage([]byte("okok"), mockSequence))

	err = sync.waitForFlush(time.Millisecond * 10)
	assert.Error(err)
}

func TestFlushing(t *testing.T) {
	assert := assert.New(t)

	sync := NewMockSync()
	batcher, err := New(sync, time.Hour, 2000000, 1024*1024)
	assert.NoError(err)

	t.Log("Calling flush sends pending messages")
	assert.NoError(batcher.AddMessage([]byte("hihi"), mockSequence))

	err = sync.waitForFlush(time.Millisecond * 10)
	assert.Error(err)

	batcher.Flush()

	err = sync.waitForFlush(time.Millisecond * 10)
	assert.NoError(err)

	assert.Equal(1, len(sync.batches))
	assert.Equal(1, len(sync.batches[0]))
	assert.Equal("hihi", string(sync.batches[0][0]))
}

func TestSendingEmpty(t *testing.T) {
	assert := assert.New(t)

	sync := NewMockSync()
	batcher, err := New(sync, time.Second, 10, 1024*1024)
	assert.NoError(err)

	t.Log("An error is returned when an empty message is sent")
	err = batcher.AddMessage([]byte{}, mockSequence)
	assert.Error(err)
	assert.Equal(err.Error(), "Empty messages can't be sent")
}

func TestUpdatingSequence(t *testing.T) {
	assert := assert.New(t)

	sync := NewMockSync()
	b, err := New(sync, time.Second, 10, 1024*1024)
	assert.NoError(err)

	batcher := b.(*batcher)

	t.Log("Initally, smallestSeq is undefined")
	assert.Nil(batcher.SmallestSequencePair().Sequence)

	expected := new(big.Int)

	t.Log("After AddMessage (seq=1), smallestSeq = 1")
	batcher.updateSequenceNumbers(SequencePair{big.NewInt(1), 1234})
	expected.SetInt64(1)
	seq := batcher.SmallestSequencePair()
	assert.True(expected.Cmp(seq.Sequence) == 0)

	t.Log("After AddMessage (seq=2), smallestSeq = 1 -- not updated because higher")
	batcher.updateSequenceNumbers(SequencePair{big.NewInt(2), 1234})
	seq = batcher.SmallestSequencePair()
	assert.True(expected.Cmp(seq.Sequence) == 0)

	t.Log("After AddMessage (seq=1), smallestSeq = 0")
	batcher.updateSequenceNumbers(SequencePair{big.NewInt(0), 1234})
	expected.SetInt64(0)
	seq = batcher.SmallestSequencePair()
	assert.True(expected.Cmp(seq.Sequence) == 0)

	t.Log("Flushing batch clears smallest sequence pair")
	assert.NoError(batcher.AddMessage([]byte("cdcd"), SequencePair{big.NewInt(2), 1234}))
	sync.waitForFlush(time.Minute)
	assert.Nil(batcher.SmallestSequencePair().Sequence)
}

func TestSequencePairIsLessThan(t *testing.T) {
	assert := assert.New(t)

	big10 := big.NewInt(10)
	big5 := big.NewInt(5)

	tests := []struct {
		left   SequencePair
		right  SequencePair
		isLess bool
	}{
		{left: SequencePair{nil, 0}, right: SequencePair{nil, 0}, isLess: false},
		{left: SequencePair{nil, 0}, right: SequencePair{big10, 0}, isLess: false},
		{left: SequencePair{big10, 0}, right: SequencePair{nil, 0}, isLess: false},

		{left: SequencePair{big5, 0}, right: SequencePair{big10, 0}, isLess: true},
		{left: SequencePair{big5, 0}, right: SequencePair{big5, 10}, isLess: true},

		{left: SequencePair{big10, 0}, right: SequencePair{big5, 0}, isLess: false},
		{left: SequencePair{big5, 10}, right: SequencePair{big5, 0}, isLess: false},
	}

	for _, test := range tests {
		left := test.left
		right := test.right
		t.Logf(
			"Is <%s, %d> less than <%s, %d>? %t",
			left.Sequence.String(), left.SubSequence,
			right.Sequence.String(), right.SubSequence,
			test.isLess,
		)

		assert.Equal(test.isLess, left.IsLessThan(right))
	}
}

func TestSequencePairEmpty(t *testing.T) {
	assert := assert.New(t)

	assert.True(SequencePair{nil, 0}.IsEmpty())
	assert.True(SequencePair{nil, 10000}.IsEmpty())

	assert.False(SequencePair{big.NewInt(10), 0}.IsEmpty())
	assert.False(SequencePair{big.NewInt(0), 1000}.IsEmpty())
}