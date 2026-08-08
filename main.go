package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

func main() {
	capacity := flag.Int("cap", 128, "缓存容量")
	ttl := flag.Duration("ttl", 0, "默认过期时间，0 表示不过期，例如 5s、2m")
	quiet := flag.Bool("q", false, "只输出结果，不打印每条命令的回显")
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, `go-lrucache - 带过期时间的 LRU 缓存，交互式命令行

从标准输入读命令，一行一条:
  set <键> <值>     写入
  get <键>          读取
  has <键>          只看在不在，不影响 LRU 顺序
  del <键>          删除
  keys              按最近使用顺序列出所有键
  purge             清掉已过期的
  clear             全清
  stats             看命中率之类的统计
  resize <数字>     调整容量

例子:
  echo set a 1 | go-lrucache -cap 2

参数:
`)
		flag.PrintDefaults()
	}
	flag.Parse()

	c, err := New(*capacity)
	if err != nil {
		fmt.Fprintln(os.Stderr, "出错:", err)
		os.Exit(1)
	}

	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if out, quit := exec(c, line, *ttl); quit {
			break
		} else if out != "" && !*quiet {
			fmt.Println(out)
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "读输入出错:", err)
		os.Exit(1)
	}
}

// 返回要打印的内容和是否退出
func exec(c *Cache, line string, defTTL time.Duration) (string, bool) {
	parts := strings.Fields(line)
	switch parts[0] {
	case "set":
		if len(parts) < 3 {
			return "用法: set <键> <值>", false
		}
		// 值里可能有空格，后面全算值
		c.SetTTL(parts[1], strings.Join(parts[2:], " "), defTTL)
		return "OK", false

	case "get":
		if len(parts) != 2 {
			return "用法: get <键>", false
		}
		if v, ok := c.Get(parts[1]); ok {
			return v, false
		}
		return "(没有)", false

	case "has":
		if len(parts) != 2 {
			return "用法: has <键>", false
		}
		if c.Has(parts[1]) {
			return "有", false
		}
		return "没有", false

	case "del":
		if len(parts) != 2 {
			return "用法: del <键>", false
		}
		if c.Delete(parts[1]) {
			return "已删", false
		}
		return "(没有)", false

	case "keys":
		ks := c.Keys()
		if len(ks) == 0 {
			return "(空)", false
		}
		return strings.Join(ks, "\n"), false

	case "purge":
		return fmt.Sprintf("清掉 %d 条过期的", c.Purge()), false

	case "clear":
		c.Clear()
		return "已清空", false

	case "resize":
		if len(parts) != 2 {
			return "用法: resize <数字>", false
		}
		n, err := strconv.Atoi(parts[1])
		if err != nil {
			return "容量得是数字", false
		}
		if err := c.Resize(n); err != nil {
			return "出错: " + err.Error(), false
		}
		return fmt.Sprintf("容量改成 %d", n), false

	case "stats":
		s := c.Stats()
		return fmt.Sprintf("条目 %d/%d  命中 %d  未命中 %d  命中率 %.1f%%  淘汰 %d  过期 %d",
			s.Len, s.Cap, s.Hits, s.Misses, s.HitRate(), s.Evicted, s.Expired), false

	case "quit", "exit":
		return "", true
	}
	return "不认识的命令: " + parts[0], false
}
