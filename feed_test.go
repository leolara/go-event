// Copyright 2016 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package event

import (
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/leolara/go-event/testing/assert"
)

func TestFeed(t *testing.T) {
	var feed Feed[int]
	var done, subscribed sync.WaitGroup
	subscriber := func(i int) {
		defer done.Done()

		subchan := make(chan int)
		sub := feed.Subscribe(subchan)
		timeout := time.NewTimer(2 * time.Second)
		subscribed.Done()

		select {
		case v := <-subchan:
			if v != 1 {
				t.Errorf("%d: received value %d, want 1", i, v)
			}
		case <-timeout.C:
			t.Errorf("%d: receive timeout", i)
		}

		sub.Unsubscribe()
		select {
		case _, ok := <-sub.Err():
			if ok {
				t.Errorf("%d: error channel not closed after unsubscribe", i)
			}
		case <-timeout.C:
			t.Errorf("%d: unsubscribe timeout", i)
		}
	}

	const n = 1000
	done.Add(n)
	subscribed.Add(n)
	for i := 0; i < n; i++ {
		go subscriber(i)
	}
	subscribed.Wait()
	if nsent := feed.Send(1); nsent != n {
		t.Errorf("first send delivered %d times, want %d", nsent, n)
	}
	if nsent := feed.Send(2); nsent != 0 {
		t.Errorf("second send delivered %d times, want 0", nsent)
	}
	done.Wait()
}

func TestFeedSubscribeSameChannel(t *testing.T) {
	var (
		feed Feed[int]
		done sync.WaitGroup
		ch   = make(chan int)
		sub1 = feed.Subscribe(ch)
		sub2 = feed.Subscribe(ch)
		_    = feed.Subscribe(ch)
	)
	expectSends := func(value, n int) {
		if nsent := feed.Send(value); nsent != n {
			t.Errorf("send delivered %d times, want %d", nsent, n)
		}
		done.Done()
	}
	expectRecv := func(wantValue, n int) {
		for i := 0; i < n; i++ {
			if v := <-ch; v != wantValue {
				t.Errorf("received %d, want %d", v, wantValue)
			}
		}
	}

	done.Add(1)
	go expectSends(1, 3)
	expectRecv(1, 3)
	done.Wait()

	sub1.Unsubscribe()

	done.Add(1)
	go expectSends(2, 2)
	expectRecv(2, 2)
	done.Wait()

	sub2.Unsubscribe()

	done.Add(1)
	go expectSends(3, 1)
	expectRecv(3, 1)
	done.Wait()
}

func TestFeedSubscribeBlockedPost(_ *testing.T) {
	var (
		feed   Feed[int]
		nsends = 2000
		ch1    = make(chan int)
		ch2    = make(chan int)
		wg     sync.WaitGroup
	)
	defer wg.Wait()

	feed.Subscribe(ch1)
	wg.Add(nsends)
	for i := 0; i < nsends; i++ {
		go func() {
			feed.Send(99)
			wg.Done()
		}()
	}

	sub2 := feed.Subscribe(ch2)
	defer sub2.Unsubscribe()

	// We're done when ch1 has received N times.
	// The number of receives on ch2 depends on scheduling.
	for i := 0; i < nsends; {
		select {
		case <-ch1:
			i++
		case <-ch2:
		}
	}
}

func TestFeedUnsubscribeBlockedPost(_ *testing.T) {
	var (
		feed   Feed[int]
		nsends = 200
		chans  = make([]chan int, 2000)
		subs   = make([]Subscription, len(chans))
		bchan  = make(chan int)
		bsub   = feed.Subscribe(bchan)
		wg     sync.WaitGroup
	)
	for i := range chans {
		chans[i] = make(chan int, nsends)
	}

	// Queue up some Sends. None of these can make progress while bchan isn't read.
	wg.Add(nsends)
	for i := 0; i < nsends; i++ {
		go func() {
			feed.Send(99)
			wg.Done()
		}()
	}
	// Subscribe the other channels.
	for i, ch := range chans {
		subs[i] = feed.Subscribe(ch)
	}
	// Unsubscribe them again.
	for _, sub := range subs {
		sub.Unsubscribe()
	}
	// Unblock the Sends.
	bsub.Unsubscribe()
	wg.Wait()
}

// Checks that unsubscribing a channel during Send works even if that
// channel has already been sent on.
func TestFeedUnsubscribeSentChan(_ *testing.T) {
	var (
		feed Feed[int]
		ch1  = make(chan int)
		ch2  = make(chan int)
		sub1 = feed.Subscribe(ch1)
		sub2 = feed.Subscribe(ch2)
		wg   sync.WaitGroup
	)
	defer sub2.Unsubscribe()

	wg.Add(1)
	go func() {
		feed.Send(0)
		wg.Done()
	}()

	// Wait for the value on ch1.
	<-ch1
	// Unsubscribe ch1, removing it from the send cases.
	sub1.Unsubscribe()

	// Receive ch2, finishing Send.
	<-ch2
	wg.Wait()

	// Send again. This should send to ch2 only, so the wait group will unblock
	// as soon as a value is received on ch2.
	wg.Add(1)
	go func() {
		feed.Send(0)
		wg.Done()
	}()
	<-ch2
	wg.Wait()
}

func TestFeedUnsubscribeFromInbox(t *testing.T) {
	var (
		feed Feed[int]
		ch1  = make(chan int)
		ch2  = make(chan int)
		sub1 = feed.Subscribe(ch1)
		sub2 = feed.Subscribe(ch1)
		sub3 = feed.Subscribe(ch2)
	)
	assert.Equal(t, 3, len(feed.inbox))
	assert.Equal(t, 1, len(feed.sendCases), "sendCases is non-empty after unsubscribe")

	sub1.Unsubscribe()
	sub2.Unsubscribe()
	sub3.Unsubscribe()
	assert.Equal(t, 0, len(feed.inbox), "Inbox is non-empty after unsubscribe")
	assert.Equal(t, 1, len(feed.sendCases), "sendCases is non-empty after unsubscribe")
}

func BenchmarkFeedSend1000(b *testing.B) {
	var (
		done  sync.WaitGroup
		feed  Feed[int]
		nsubs = 1000
	)
	subscriber := func(ch <-chan int) {
		for i := 0; i < b.N; i++ {
			<-ch
		}
		done.Done()
	}
	done.Add(nsubs)
	for i := 0; i < nsubs; i++ {
		ch := make(chan int, 200)
		feed.Subscribe(ch)
		go subscriber(ch)
	}

	// The actual benchmark.
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if feed.Send(i) != nsubs {
			panic("wrong number of sends")
		}
	}

	b.StopTimer()
	done.Wait()
}

type testCase[T any] struct {
	name      string
	evFeed    *Feed[T]
	testSetup func(fd *Feed[T], t *testing.T, o interface{})
	obj       T
}

func (tt testCase[T]) run(t *testing.T) {
	t.Run(tt.name, func(t *testing.T) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("panic triggered when unexpected: %v", r)
			}
		}()
		tt.testSetup(tt.evFeed, t, tt.obj)
		if gotNsent := tt.evFeed.Send(tt.obj); gotNsent != 1 {
			t.Errorf("Send() = %v, want %v", gotNsent, 1)
		}
	})
}

func TestFeed_Send(t *testing.T) {

	testCase[testFeedWithPointer]{
		name:   "normal struct",
		evFeed: new(Feed[testFeedWithPointer]),
		testSetup: func(fd *Feed[testFeedWithPointer], t *testing.T, o interface{}) {
			testChan := make(chan testFeedWithPointer, 1)
			fd.Subscribe(testChan)
		},
		obj: testFeedWithPointer{
			a: new(uint64),
			b: new(string),
		},
	}.run(t)

	testCase[testFeedIface]{
		name:   "fully-implemented interface",
		evFeed: new(Feed[testFeedIface]),
		testSetup: func(fd *Feed[testFeedIface], t *testing.T, o interface{}) {
			testChan := make(chan testFeedIface)
			// Make it unbuffered to allow message to
			// pass through
			go func() {
				a := <-testChan
				if !reflect.DeepEqual(a, o) {
					t.Errorf("Got = %v, want = %v", a, o)
				}
			}()
			fd.Subscribe(testChan)
		},
		obj: testFeed{
			a: 0,
			b: "",
		},
	}.run(t)

	testCase[testFeedIface]{
		name:   "fully-implemented interface with additional methods",
		evFeed: new(Feed[testFeedIface]),
		testSetup: func(fd *Feed[testFeedIface], t *testing.T, o interface{}) {
			testChan := make(chan testFeedIface)
			// Make it unbuffered to allow message to
			// pass through
			go func() {
				a := <-testChan
				if !reflect.DeepEqual(a, o) {
					t.Errorf("Got = %v, want = %v", a, o)
				}
			}()
			fd.Subscribe(testChan)
		},
		obj: testFeed3{
			a: 0,
			b: "",
			c: []byte{'A'},
			d: []byte{'B'},
		},
	}.run(t)
}

// The following objects below are a collection of different
// struct types to test with.
type testFeed struct {
	a uint64
	b string
}

func (testFeed) method1() {

}

func (testFeed) method2() {

}

type testFeedWithPointer struct {
	a *uint64
	b *string
}

type testFeed2 struct {
	a uint64
	b string
	c []byte
}

func (testFeed2) method1() {

}

type testFeed3 struct {
	a    uint64
	b    string
	c, d []byte
}

func (testFeed3) method1() {

}

func (testFeed3) method2() {

}

func (testFeed3) method3() {

}

type testFeedIface interface {
	method1()
	method2()
}
