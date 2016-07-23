// Copyright (c) 2016 Couchbase, Inc.
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
// except in compliance with the License. You may obtain a copy of the License at
//   http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed under the
// License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
// either express or implied. See the License for the specific language governing permissions
// and limitations under the License.

package skiplist

import "sync/atomic"
import "unsafe"

// Iterator is used for lookup and range operations on skiplist
type Iterator struct {
	cmp        CompareFn
	s          *Skiplist
	prev, curr *Node
	valid      bool
	buf        *ActionBuffer
	deleted    bool

	bs *BarrierSession
}

// NewIterator creates an iterator for skiplist
func (s *Skiplist) NewIterator(cmp CompareFn,
	buf *ActionBuffer) *Iterator {

	return &Iterator{
		cmp: cmp,
		s:   s,
		buf: buf,
		bs:  s.barrier.Acquire(),
	}
}

// SeekFirst moves cursor to the start
func (it *Iterator) SeekFirst() {
	it.prev = it.s.head
	it.curr, _ = it.s.head.getNext(0)
	it.valid = true
}

// SeekWithCmp moves iterator to a provided item by using custom comparator
func (it *Iterator) SeekWithCmp(itm unsafe.Pointer, cmp CompareFn, eqCmp CompareFn) bool {
	var found bool
	if found = it.s.findPath(itm, cmp, it.buf, &it.s.Stats) != nil; found {
		it.prev = it.buf.preds[0]
		it.curr = it.buf.succs[0]
	} else {
		if found = eqCmp != nil && Compare(eqCmp, itm, it.buf.preds[0].Item()) == 0; found {
			it.prev = nil
			it.curr = it.buf.preds[0]
		}
	}
	return found
}

// SeekWithSkip performs Seek() with optional skipping of nodes while reading nodes as part of
// finding the item.
func (it *Iterator) SeekWithSkip(itm unsafe.Pointer, skipItm func(unsafe.Pointer) bool) bool {
	it.valid = true
	found := it.s.findPath2(itm, it.cmp, skipItm, it.buf, &it.s.Stats) != nil
	it.prev = it.buf.preds[0]
	it.curr = it.buf.succs[0]
	return found
}

// Seek moves iterator to a provided item
func (it *Iterator) Seek(itm unsafe.Pointer) bool {
	return it.SeekWithSkip(itm, nil)
}

// SeekPrev moves iterator to the provided item or an item less than the lookup item
func (it *Iterator) SeekPrev(itm unsafe.Pointer, skip func(unsafe.Pointer) bool) {
	if !it.SeekWithSkip(itm, skip) && it.prev != it.s.head {
		it.curr = it.prev
		it.prev = nil
	}
}

// Valid returns true when iterator reaches the end
// If the specified item is not found, start with the predecessor node
// This is used for implementing disk block based storage
func (it *Iterator) Valid() bool {
	if it.valid && it.curr == it.s.tail {
		it.valid = false
	}

	return it.valid
}

// Get returns the current item
func (it *Iterator) Get() unsafe.Pointer {
	return it.curr.Item()
}

// GetNode returns node which holds the current item
func (it *Iterator) GetNode() *Node {
	return it.curr
}

// Delete removes the current item from the skiplist
func (it *Iterator) Delete() {
	it.s.softDelete(it.curr, &it.s.Stats)
	// It will observe that current item is deleted
	// Run delete helper and move to the next possible item
	it.Next()
	it.deleted = true
}

// Next moves iterator to the next item
func (it *Iterator) Next() {
	if it.deleted {
		it.deleted = false
		return
	}

	if !it.Valid() {
		return
	}

retry:
	it.valid = true
	next, deleted := it.curr.getNext(0)
	if deleted {
		// Current node is deleted. Unlink current node from the level
		// and make next node as current node.
		// If it fails, refresh the path buffer and obtain new current node.
		if it.prev != nil && it.s.helpDelete(0, it.prev, it.curr, next, &it.s.Stats) {
			it.curr = next
		} else {
			atomic.AddUint64(&it.s.Stats.readConflicts, 1)
			found := it.s.findPath(it.curr.Item(), it.cmp, it.buf, &it.s.Stats) != nil
			last := it.curr
			it.prev = it.buf.preds[0]
			it.curr = it.buf.succs[0]
			if found && last == it.curr {
				goto retry
			}
		}
	} else {
		it.prev = it.curr
		it.curr = next
	}
}

// Close is a destructor
func (it *Iterator) Close() {
	it.s.barrier.Release(it.bs)
}
