package main

import (
	"container/list"
	"errors"
	"sync"
	"time"
)

type entry struct {
	key      string
	value    string
	expireAt time.Time // 零值表示不过期
}

// 定长 LRU，支持每条单独设过期时间
type Cache struct {
	mu    sync.Mutex
	cap   int
	ll    *list.List               // 队头是最近用过的
	items map[string]*list.Element // key 到链表节点，省得遍历

	hits    int64
	misses  int64
	evicted int64
	expired int64

	now func() time.Time
}

func New(capacity int) (*Cache, error) {
	if capacity <= 0 {
		return nil, errors.New("容量要大于 0")
	}
	return &Cache{
		cap:   capacity,
		ll:    list.New(),
		items: make(map[string]*list.Element, capacity),
		now:   time.Now,
	}, nil
}

func (c *Cache) Set(key, value string) {
	c.SetTTL(key, value, 0)
}

// ttl 传 0 或负数表示不过期
func (c *Cache) SetTTL(key, value string, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var exp time.Time
	if ttl > 0 {
		exp = c.now().Add(ttl)
	}

	// 已经在里面就地改，顺便提到队头
	if el, ok := c.items[key]; ok {
		e := el.Value.(*entry)
		e.value = value
		e.expireAt = exp
		c.ll.MoveToFront(el)
		return
	}

	el := c.ll.PushFront(&entry{key: key, value: value, expireAt: exp})
	c.items[key] = el

	// 超了就从队尾踢，一次可能不止一个（容量被调小过）
	for c.ll.Len() > c.cap {
		c.evictOldest()
	}
}

func (c *Cache) evictOldest() {
	el := c.ll.Back()
	if el == nil {
		return
	}
	c.ll.Remove(el)
	delete(c.items, el.Value.(*entry).key)
	c.evicted++
}

func (c *Cache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		c.misses++
		return "", false
	}

	e := el.Value.(*entry)
	// 过期的当没命中，顺手清掉，不然会一直占位置
	if !e.expireAt.IsZero() && !c.now().Before(e.expireAt) {
		c.ll.Remove(el)
		delete(c.items, key)
		c.expired++
		c.misses++
		return "", false
	}

	c.ll.MoveToFront(el)
	c.hits++
	return e.value, true
}

// 只看在不在，不动 LRU 顺序也不计入命中率
func (c *Cache) Has(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return false
	}
	e := el.Value.(*entry)
	return e.expireAt.IsZero() || c.now().Before(e.expireAt)
}

func (c *Cache) Delete(key string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[key]
	if !ok {
		return false
	}
	c.ll.Remove(el)
	delete(c.items, key)
	return true
}

// 主动扫一遍清掉过期的，返回清了几个
func (c *Cache) Purge() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	n := 0
	// 边遍历边删链表，得先存好 next
	for el := c.ll.Front(); el != nil; {
		next := el.Next()
		e := el.Value.(*entry)
		if !e.expireAt.IsZero() && !now.Before(e.expireAt) {
			c.ll.Remove(el)
			delete(c.items, e.key)
			c.expired++
			n++
		}
		el = next
	}
	return n
}

func (c *Cache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ll.Init()
	c.items = make(map[string]*list.Element, c.cap)
}

// Len 只数没过期的
func (c *Cache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	n := 0
	for _, el := range c.items {
		e := el.Value.(*entry)
		if e.expireAt.IsZero() || now.Before(e.expireAt) {
			n++
		}
	}
	return n
}

// 从最近用的到最久没用的
func (c *Cache) Keys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	out := make([]string, 0, c.ll.Len())
	for el := c.ll.Front(); el != nil; el = el.Next() {
		e := el.Value.(*entry)
		if e.expireAt.IsZero() || now.Before(e.expireAt) {
			out = append(out, e.key)
		}
	}
	return out
}

// 调容量。调小的话立刻踢掉多出来的
func (c *Cache) Resize(n int) error {
	if n <= 0 {
		return errors.New("容量要大于 0")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cap = n
	for c.ll.Len() > c.cap {
		c.evictOldest()
	}
	return nil
}

type Stats struct {
	Hits, Misses, Evicted, Expired int64
	Len, Cap                       int
}

func (c *Cache) Stats() Stats {
	c.mu.Lock()
	s := Stats{
		Hits: c.hits, Misses: c.misses,
		Evicted: c.evicted, Expired: c.expired,
		Cap: c.cap,
	}
	c.mu.Unlock()
	s.Len = c.Len()
	return s
}

func (s Stats) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0
	}
	return float64(s.Hits) / float64(total) * 100
}
