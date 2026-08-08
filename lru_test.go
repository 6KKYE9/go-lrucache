package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newTestCache(t *testing.T, n int) (*Cache, *fakeClock) {
	t.Helper()
	c, err := New(n)
	if err != nil {
		t.Fatal(err)
	}
	clk := newClock()
	c.now = clk.Now
	return c, clk
}

func TestSetGet(t *testing.T) {
	c, _ := newTestCache(t, 3)
	c.Set("a", "1")
	if v, ok := c.Get("a"); !ok || v != "1" {
		t.Errorf("拿不到 a: %q %v", v, ok)
	}
	if _, ok := c.Get("nope"); ok {
		t.Error("不存在的键不该命中")
	}
}

func TestEvictLeastRecent(t *testing.T) {
	c, _ := newTestCache(t, 2)
	c.Set("a", "1")
	c.Set("b", "2")
	c.Get("a") // a 变成最近用过的
	c.Set("c", "3")

	if _, ok := c.Get("b"); ok {
		t.Error("b 是最久没用的，该被踢掉")
	}
	if _, ok := c.Get("a"); !ok {
		t.Error("a 刚用过，不该被踢")
	}
}

// 重复写同一个 key 不该占两个位置
func TestOverwriteNoGrow(t *testing.T) {
	c, _ := newTestCache(t, 2)
	c.Set("a", "1")
	c.Set("a", "2")
	c.Set("b", "3")
	if c.Len() != 2 {
		t.Errorf("该只有 2 条，得到 %d", c.Len())
	}
	if v, _ := c.Get("a"); v != "2" {
		t.Errorf("a 该被覆盖成 2，得到 %q", v)
	}
}

func TestTTLExpire(t *testing.T) {
	c, clk := newTestCache(t, 5)
	c.SetTTL("a", "1", time.Minute)
	if _, ok := c.Get("a"); !ok {
		t.Fatal("刚写的不该过期")
	}
	clk.Advance(2 * time.Minute)
	if _, ok := c.Get("a"); ok {
		t.Error("过期了还能拿到")
	}
}

// 过期的条目 Get 之后要真删掉，不能一直占坑
func TestExpiredFreesSlot(t *testing.T) {
	c, clk := newTestCache(t, 2)
	c.SetTTL("a", "1", time.Second)
	c.Set("b", "2")
	clk.Advance(2 * time.Second)

	c.Get("a") // 触发清理
	c.Set("cc", "3")
	if _, ok := c.Get("b"); !ok {
		t.Error("a 过期腾出位置了，b 不该被挤掉")
	}
}

// Len 不能把已过期的算进去
func TestLenSkipsExpired(t *testing.T) {
	c, clk := newTestCache(t, 5)
	c.SetTTL("a", "1", time.Second)
	c.Set("b", "2")
	clk.Advance(2 * time.Second)
	if c.Len() != 1 {
		t.Errorf("该只剩 1 条有效的，Len 返回 %d", c.Len())
	}
}

func TestPurge(t *testing.T) {
	c, clk := newTestCache(t, 10)
	c.SetTTL("a", "1", time.Second)
	c.SetTTL("b", "2", time.Second)
	c.Set("c", "3")
	clk.Advance(2 * time.Second)

	if n := c.Purge(); n != 2 {
		t.Errorf("该清掉 2 条，实际 %d", n)
	}
	if c.Len() != 1 {
		t.Errorf("该剩 1 条，得到 %d", c.Len())
	}
	if _, ok := c.Get("c"); !ok {
		t.Error("没过期的 c 被误删了")
	}
}

// Has 不该改变 LRU 顺序
func TestHasDoesNotTouchOrder(t *testing.T) {
	c, _ := newTestCache(t, 2)
	c.Set("a", "1")
	c.Set("b", "2")
	c.Has("a")
	c.Set("c", "3")
	if _, ok := c.Get("a"); ok {
		t.Error("Has 不该把 a 提到队头")
	}
}

func TestHasExpired(t *testing.T) {
	c, clk := newTestCache(t, 5)
	c.SetTTL("a", "1", time.Second)
	clk.Advance(2 * time.Second)
	if c.Has("a") {
		t.Error("过期的 Has 该返回 false")
	}
}

func TestKeysOrder(t *testing.T) {
	c, _ := newTestCache(t, 5)
	c.Set("a", "1")
	c.Set("b", "2")
	c.Set("c", "3")
	c.Get("a")
	got := strings.Join(c.Keys(), ",")
	if got != "a,c,b" {
		t.Errorf("顺序该是 a,c,b，得到 %s", got)
	}
}

// 容量调小要立刻把多出来的踢掉
func TestResizeShrink(t *testing.T) {
	c, _ := newTestCache(t, 5)
	for i := 0; i < 5; i++ {
		c.Set(fmt.Sprintf("k%d", i), "v")
	}
	if err := c.Resize(2); err != nil {
		t.Fatal(err)
	}
	if c.Len() != 2 {
		t.Errorf("调小后该剩 2 条，得到 %d", c.Len())
	}
	// 留下的该是最近写的两个
	if _, ok := c.Get("k4"); !ok {
		t.Error("最近写的 k4 不该被踢")
	}
	if _, ok := c.Get("k0"); ok {
		t.Error("最老的 k0 该被踢掉")
	}
}

func TestBadCapacity(t *testing.T) {
	if _, err := New(0); err == nil {
		t.Error("容量 0 该报错")
	}
	if _, err := New(-3); err == nil {
		t.Error("负容量该报错")
	}
	c, _ := newTestCache(t, 2)
	if err := c.Resize(0); err == nil {
		t.Error("Resize 到 0 该报错")
	}
}

func TestDeleteAndClear(t *testing.T) {
	c, _ := newTestCache(t, 5)
	c.Set("a", "1")
	if !c.Delete("a") {
		t.Error("删已有的键该返回 true")
	}
	if c.Delete("a") {
		t.Error("重复删该返回 false")
	}
	c.Set("b", "2")
	c.Clear()
	if c.Len() != 0 {
		t.Errorf("清空后该是 0，得到 %d", c.Len())
	}
	if _, ok := c.Get("b"); ok {
		t.Error("清空后还能拿到")
	}
}

func TestStats(t *testing.T) {
	c, _ := newTestCache(t, 1)
	c.Set("a", "1")
	c.Get("a")
	c.Get("nope")
	c.Set("b", "2") // 挤掉 a

	s := c.Stats()
	if s.Hits != 1 || s.Misses != 1 {
		t.Errorf("命中统计不对: %+v", s)
	}
	if s.Evicted != 1 {
		t.Errorf("淘汰数该是 1，得到 %d", s.Evicted)
	}
	if s.HitRate() != 50 {
		t.Errorf("命中率该是 50%%，得到 %.1f", s.HitRate())
	}
}

// 一次都没访问过的时候别除以 0
func TestHitRateZero(t *testing.T) {
	c, _ := newTestCache(t, 2)
	if r := c.Stats().HitRate(); r != 0 {
		t.Errorf("没访问过命中率该是 0，得到 %v", r)
	}
}

// 并发写不能撑爆容量。本机跑不了 -race，只能断言最终大小
func TestConcurrentCapacity(t *testing.T) {
	c, _ := newTestCache(t, 50)
	var wg sync.WaitGroup
	for i := 0; i < 400; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c.Set(fmt.Sprintf("k%d", i), "v")
			c.Get(fmt.Sprintf("k%d", i/2))
		}(i)
	}
	wg.Wait()
	if c.Len() > 50 {
		t.Errorf("并发写撑爆了容量: %d", c.Len())
	}
}

// 走一遍命令行的解析，别让库层校验被上层绕过
func TestExecCommands(t *testing.T) {
	c, _ := newTestCache(t, 2)
	if out, _ := exec(c, "set a hello world", 0); out != "OK" {
		t.Errorf("set 失败: %s", out)
	}
	// 值里带空格得完整保留
	if out, _ := exec(c, "get a", 0); out != "hello world" {
		t.Errorf("带空格的值被截断了: %q", out)
	}
	if out, _ := exec(c, "resize abc", 0); !strings.Contains(out, "数字") {
		t.Errorf("resize 非数字该提示: %s", out)
	}
	if out, _ := exec(c, "resize 0", 0); !strings.Contains(out, "出错") {
		t.Errorf("resize 0 该被库层拦下: %s", out)
	}
	if _, quit := exec(c, "quit", 0); !quit {
		t.Error("quit 该退出")
	}
}
