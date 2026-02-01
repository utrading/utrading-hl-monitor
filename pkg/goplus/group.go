package goplus

import (
	"sync"
	"sync/atomic"
)

var (
	defaultGroup     *WaitGroup
	defaultGroupOnce sync.Once
)

func DefaultGroup() *WaitGroup {
	defaultGroupOnce.Do(func() {
		defaultGroup = NewWaitGroup()
	})
	return defaultGroup
}

func Go(fn func()) {
	DefaultGroup().Go(fn)
}

func Add() {
	DefaultGroup().Add()
}

func Done() {
	DefaultGroup().Done()
}

func Wait() {
	DefaultGroup().Wait()
}

type WaitGroup struct {
	wg             sync.WaitGroup
	CurrentGoCount atomic.Int64
}

func NewWaitGroup() *WaitGroup {
	return &WaitGroup{
		wg: sync.WaitGroup{},
	}
}

func (s *WaitGroup) Go(fn func()) {
	s.Add()

	go func() {
		defer Recover()
		defer s.Done()

		fn()
	}()
}

func (s *WaitGroup) Add() {
	s.CurrentGoCount.Add(1)
	s.wg.Add(1)
}

func (s *WaitGroup) Done() {
	s.CurrentGoCount.Add(-1)
	s.wg.Done()
}

func (s *WaitGroup) Wait() {
	s.wg.Wait()
}
