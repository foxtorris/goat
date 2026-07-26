# Streaming SDK

`streaming` 提供一个基于 Go 泛型和 Channel 的轻量级、并发安全数据流抽象，适合在生产者与消费者、异步任务、Agent 执行过程之间传递类型安全的数据。

模块仅依赖 Go 标准库，支持缓冲与无缓冲模式、阻塞与非阻塞写入、超时、Context 取消、批量读写以及简单的流转换组合。

## 功能概览

- 使用 Go 泛型提供编译期类型安全。
- 支持缓冲流与无缓冲流。
- 支持阻塞读取、超时读取和 Context 读取。
- 支持阻塞写入、非阻塞写入、超时写入和 Context 写入。
- 支持幂等关闭，并允许关闭后继续读完缓冲区中的数据。
- 支持 `Transform`、`Filter` 和 `Merge` 流组合。
- 支持 `ReadAll` 与 `WriteAll` 批量操作。

## 安装

```bash
go get github.com/torrischen/goat/streaming
```

## 快速开始

```go
package main

import (
	"errors"
	"fmt"
	"log"

	"github.com/torrischen/goat/streaming"
)

func main() {
	stream := streaming.NewStream[int](4)

	go func() {
		defer stream.Close()
		for i := 1; i <= 5; i++ {
			if err := stream.Write(i); err != nil {
				log.Printf("write: %v", err)
				return
			}
		}
	}()

	for {
		value, err := stream.Read()
		if errors.Is(err, streaming.ErrStreamClosed) {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(value)
	}
}
```

生产者通常负责关闭流，消费者持续读取直到收到 `ErrStreamClosed`。`Close` 不会丢弃已经写入缓冲区的数据。

## 核心接口

```go
type Stream[T any] interface {
	Read() (T, error)
	ReadWithTimeout(time.Duration) (T, error)
	ReadWithContext(context.Context) (T, error)
	Write(T) error
	WriteWithTimeout(T, time.Duration) error
	WriteWithContext(context.Context, T) error
	Close() error
	IsClosed() bool
	Len() int
}
```

`NewStream` 返回具体类型 `*StreamImpl[T]`。除接口方法外，具体类型还提供 `TryWrite`、`ReadAll` 和 `WriteAll`。

## 创建 Stream

### 缓冲流

```go
stream := streaming.NewStream[string](16)
```

只要缓冲区未满，写入方就不需要等待消费者。缓冲大小为 `0` 时行为与无缓冲 Channel 相同。

### 无缓冲流

```go
stream := streaming.NewUnbufferedStream[string]()
```

无缓冲流用于生产者和消费者同步交接数据：写入会等待某个消费者读取。

## 读取数据

### 阻塞读取

```go
value, err := stream.Read()
```

`Read` 会一直等待以下任一情况发生：

- 读取到一条数据；
- Stream 已关闭且缓冲区已读空，此时返回 `ErrStreamClosed`。

### 超时读取

```go
value, err := stream.ReadWithTimeout(2 * time.Second)
if errors.Is(err, context.DeadlineExceeded) {
	// 在超时时间内没有读到数据。
}
```

当前实现使用 `context.WithTimeout`，超时时返回 `context.DeadlineExceeded`。

### Context 读取

```go
ctx, cancel := context.WithCancel(context.Background())
defer cancel()

value, err := stream.ReadWithContext(ctx)
if errors.Is(err, context.Canceled) {
	// 调用方取消了等待。
}
```

Context 只取消本次等待，不会自动关闭 Stream。

### 读取全部数据

```go
items, err := stream.ReadAll()
```

`ReadAll` 会持续读取，直到 Stream 被关闭并且缓冲区被读空。因此，调用前必须确保某个生产者最终会调用 `Close`，否则它可能永久阻塞。

## 写入数据

### 阻塞写入

```go
err := stream.Write(value)
```

当缓冲区已满或 Stream 无缓冲时，`Write` 会等待可用消费者；Stream 已关闭时返回 `ErrStreamClosed`。

### 非阻塞写入

```go
err := stream.TryWrite(value)
switch {
case err == nil:
	// 写入成功。
case errors.Is(err, streaming.ErrWouldBlock):
	// 当前没有可用容量或消费者。
case errors.Is(err, streaming.ErrStreamClosed):
	// Stream 已关闭。
}
```

`TryWrite` 是 `StreamImpl` 的扩展方法，不属于 `Stream` 接口。

### 超时与 Context 写入

```go
err := stream.WriteWithTimeout(value, time.Second)

ctx, cancel := context.WithTimeout(context.Background(), time.Second)
defer cancel()
err = stream.WriteWithContext(ctx, value)
```

超时返回 `context.DeadlineExceeded`，主动取消返回 `context.Canceled`。Context 取消同样不会关闭 Stream。

### 批量写入

```go
err := stream.WriteAll([]int{1, 2, 3})
```

`WriteAll` 按顺序调用 `Write`。发生首个错误时立即返回，之前写入的数据不会回滚。

## 生命周期

```go
stream := streaming.NewStream[int](8)

fmt.Println(stream.IsClosed()) // false
fmt.Println(stream.Len())      // 当前缓冲区中的元素数量

_ = stream.Close()
_ = stream.Close() // Close 是幂等的
```

需要注意：

- `Close` 只关闭 Stream 的生命周期信号，不直接关闭内部数据 Channel。
- 关闭后禁止继续写入，但消费者仍可读取缓冲区中的剩余数据。
- `Len` 返回当前缓冲区长度，不包含正在处理或等待写入的数据。
- `IsClosed` 只表示已调用 `Close`，不表示缓冲区已读空。
- 多个协程可以并发读取或写入，但应明确由谁负责最终关闭。

## 流转换

### Transform

```go
source := streaming.NewStream[int](4)
squares := streaming.Transform[int, int](source, func(value int) int {
	return value * value
})
```

`Transform` 为每条源数据调用转换函数，并返回一个新的 Stream。源 Stream 关闭并读空后，目标 Stream 自动关闭。

### Filter

```go
evens := streaming.Filter[int](source, func(value int) bool {
	return value%2 == 0
})
```

只有满足 Predicate 的数据会被写入目标 Stream。

### Merge

```go
merged := streaming.Merge[int](streamA, streamB, streamC)
```

`Merge` 并发读取所有输入 Stream。当所有输入 Stream 都关闭并读空后，合并后的 Stream 自动关闭。不同输入之间的输出顺序不保证稳定，但同一个输入 Stream 内的数据顺序会被保留。

### 组合示例

```go
source := streaming.NewStream[int](8)

filtered := streaming.Filter[int](source, func(value int) bool {
	return value%2 == 0
})
labels := streaming.Transform[int, string](filtered, func(value int) string {
	return fmt.Sprintf("even:%d", value)
})

go func() {
	defer source.Close()
	_ = source.WriteAll([]int{1, 2, 3, 4})
}()

items, err := labels.(*streaming.StreamImpl[string]).ReadAll()
```

由于 `Transform`、`Filter` 和 `Merge` 返回 `Stream[T]` 接口，通常直接循环读取即可。只有确实需要扩展方法时才应做具体类型断言。

## 错误说明

| 错误 | 触发场景 |
| --- | --- |
| `ErrStreamClosed` | Stream 已关闭，无法写入；或已关闭且没有剩余数据可读。 |
| `ErrWouldBlock` | `TryWrite` 当前无法立即完成写入。 |
| `context.DeadlineExceeded` | 超时读取或写入超过指定时间。 |
| `context.Canceled` | 传入的 Context 被主动取消。 |

包中保留了 `ErrTimeout`，但当前 `ReadWithTimeout` 和 `WriteWithTimeout` 实际返回的是 `context.DeadlineExceeded`。调用方应优先使用 `errors.Is` 判断 Context 错误。

## 最佳实践

- 明确生产者拥有关闭权，避免多个业务方互相等待。
- 使用 `defer stream.Close()` 保证生产者退出时关闭 Stream。
- 长生命周期任务优先使用 `ReadWithContext` 和 `WriteWithContext`。
- 不要通过轮询 `IsClosed` 判断是否读取结束，应持续 `Read` 到 `ErrStreamClosed`。
- 谨慎使用无缓冲 Stream；没有消费者时，生产者会阻塞。
- `Transform` 和 `Filter` 的目标 Stream 是无缓冲的，必须及时消费下游数据，避免整条流水线阻塞。
- Transformer 和 Predicate 应避免 Panic；当前工具函数不会自动恢复用户函数中的 Panic。

## 测试

```bash
go test ./streaming
```
