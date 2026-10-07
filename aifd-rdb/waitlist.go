package rdb

import (
	"container/list"
	"time"
)

type waitEntry struct {
	op          finalIntent
	firstQueued time.Time
}

type waitList struct {
	items list.List
	index map[string]*list.Element
}

func newWaitList() *waitList {
	return &waitList{index: make(map[string]*list.Element)}
}

func (w *waitList) Len() int {
	if w == nil {
		return 0
	}
	return w.items.Len()
}

func (w *waitList) PushBack(op finalIntent) {
	entityKey := op.entityKey()
	if element, ok := w.index[entityKey]; ok {
		entry := element.Value.(waitEntry)
		entry.op = op
		element.Value = entry
		return
	}
	element := w.items.PushBack(waitEntry{op: op, firstQueued: time.Now()})
	w.index[entityKey] = element
}

func (w *waitList) PopFront() (finalIntent, bool) {
	front := w.items.Front()
	if front == nil {
		return finalIntent{}, false
	}
	entry := front.Value.(waitEntry)
	op := entry.op
	delete(w.index, op.entityKey())
	w.items.Remove(front)
	return op, true
}

func (w *waitList) PopFrontReady(now time.Time, minStay time.Duration) (finalIntent, bool) {
	front := w.items.Front()
	if front == nil {
		return finalIntent{}, false
	}
	entry := front.Value.(waitEntry)
	if minStay > 0 && now.Sub(entry.firstQueued) < minStay {
		return finalIntent{}, false
	}
	op := entry.op
	delete(w.index, op.entityKey())
	w.items.Remove(front)
	return op, true
}

func (w *waitList) Has(entityKey string) bool {
	_, ok := w.index[entityKey]
	return ok
}

func (w *waitList) Snapshot() []finalIntent {
	out := make([]finalIntent, 0, w.items.Len())
	for element := w.items.Front(); element != nil; element = element.Next() {
		out = append(out, element.Value.(waitEntry).op)
	}
	return out
}

func (w *waitList) Clear() {
	w.items.Init()
	w.index = make(map[string]*list.Element)
}
