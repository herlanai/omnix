# 会话持久化

[文档索引](README.md) · [English](../en/persistence.md)

会话状态与存储是分离的。创建 Agent 或 `SessionLog` 不会自动保存数据。应用需要选择后端、转发事件、处理写入失败并协调退出过程。

## 选择存储层

| 层 | 公开入口 | 保存内容 |
| --- | --- | --- |
| [内存日志](../../pkg/session/session.go) | `session.NewSessionLog(id)` | 当前进程中的事件和实时顺序状态。 |
| [JSONL](../../pkg/persistence/jsonl.go) | `persistence.NewJSONL(dir, batchSize, options...)` | 每个会话目录下的 `header.json` 与 `events.jsonl`。 |
| [SQLite](../../pkg/persistence/sqlite/sqlite.go) | `sqlite.Open(path)` | 以会话和序号为键的事件 JSON，以及可选全文索引。 |
| [Storage Domain](../../pkg/storage/storage.go) | `storage.NewDomain[T](backend, prefix)` | 带 CAS 版本的类型化键值记录，适用于配置类记录和会话附属数据，而非会话事件。 |

`SessionLog.Append(data)` 校验顺序、分配序号和时间戳，并更新内存。`Events()` 返回事件切片快照，不会刷新存储。后端的 `Append` 接收完整 `SessionEvent`，不能替代日志自身的生命周期校验。

JSONL 实现 `persistence.Persistence`。SQLite 使用独立 API：`Load` 只返回事件，不返回头信息，也不实现这个接口。应按需要的操作选择后端，不要假定两者可直接互换。

## 写入一个简单 JSONL 会话

以下函数创建新会话，把事件复制到 JSONL，显式刷新，并核对已保存的事件数量。对于每个新的存储目录和会话身份，只调用一次；生产写入器应记录最后持久化的序号，不应每次追加后重新发送整个日志。

```go
package sessionstore

import (
	"context"
	"errors"

	"github.com/herlanai/omnix/pkg/brand"
	"github.com/herlanai/omnix/pkg/persistence"
	"github.com/herlanai/omnix/pkg/session"
)

func WriteExample(ctx context.Context, dir string) (err error) {
	id := brand.NewSessionID("example-session")
	log := session.NewSessionLog(id)
	if _, err = log.Append(session.UserMessageData{Content: "Hello"}); err != nil {
		return err
	}
	store, err := persistence.NewJSONL(dir, 32)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, store.Close()) }()
	if err = store.SaveHeader(ctx, session.NewSessionHeader(id, ".")); err != nil {
		return err
	}
	for _, event := range log.Events() {
		if err = store.Append(ctx, id, event); err != nil {
			return err
		}
	}
	if err = store.Flush(ctx, id); err != nil {
		return err
	}
	_, events, err := store.Load(ctx, id)
	if err != nil {
		return err
	}
	if len(events) != log.Len() {
		return errors.New("stored event count differs from memory")
	}
	return nil
}
```

`SaveHeader` 是 JSONL 具体类型的方法，不属于 `Persistence` 接口。使用 [`NewSessionHeader`](../../pkg/session/header.go)设置当前格式版本。读取时会拒绝不支持的头版本；应用自身的元数据校验仍需另行处理。

## JSONL 检查点与恢复

默认有 16 个分片，刷新间隔为 100 ms。`WithShardCount(n)` 向上取整到二的幂；由于后台使用 `time.NewTicker`，`WithFlushInterval(d)` 需要正数时长。非正 batch size 会变为一。达到批量阈值只是通知后台写入器；`Append` 成功表示已接受到缓冲区，不表示磁盘持久化完成。

`Flush(ctx, id)` 等待当前分片写入器，并写入该会话的待处理事件。`FlushAll` 逐个处理分片。`Load` 只读磁盘，因此要读取刚追加的数据，应先刷新。`Snapshot` 执行刷新和读取后返回事件数量，不会创建另一种快照文件格式。

退出前先停止事件生产者，再调用 `Close` 停止并等待后台写入器。`Close` 之后不支持继续追加。JSONL 方法虽然接收 context，但目前不会用它取消文件操作或互斥锁等待。

读取未闭合的最后一轮时，可能补入工具错误结果、缺失的 step end 和 interrupted turn end，并重写存储。这是有副作用的恢复操作，不是只读查询。应在该会话没有活动写入器时执行。对于损坏 JSON 行以及超过约 1 MiB scanner 限制的行，`Load` 返回错误，不会静默截掉任意损坏尾部。

选择此后端时，应理解当前持久性边界：

- 没有 `fsync` 持久性承诺。刷新是缓冲和检查点操作，不保证断电后数据仍在。
- 后台刷新错误没有独立错误通道；即使最后一次后台刷新失败，`Close` 当前也返回 nil。
- 追加辅助方法忽略 deferred buffered-writer flush 和文件关闭错误，因此显式 `Flush` 也无法报告所有晚发生的 I/O 失败。
- 部分写入后重试批次可能造成重复；它不是 exactly-once 日志写入器。头文件写入和恢复重写也不是多文件事务。
- 后端使用原始会话 ID 构造路径。可信应用代码应生成适用于文件路径的 ID；品牌类型本身不校验路径安全。

如需严格的持久化确认或多进程协调，应补充并测试相应的存储边界，而不是将这些辅助组件视为完整日志服务。

## SQLite 写入与搜索

`Store.Append(ctx, id, event)` 写入单条事件；`AppendBatch(ctx, id, events)` 在一个 SQL 事务中写入批次，需要检查返回的提交错误。`Load` 按序号返回事件；`Count`、`ListSessions` 和 `Search` 提供查询。应用不再持有该存储时，应关闭它。

打开数据库时会尝试创建 FTS。应检查 `FTSEnabled()`，不要假设索引必然可用。索引文本来自用户消息、助手消息和会话标题；少于三个 rune 的短查询或不可用的 FTS 会回退到 `LIKE`。因此搜索语义和成本会随查询及后端能力变化。参见[搜索实现](../../pkg/persistence/sqlite/sqlite.go)与[往返和搜索测试](../../tests/persistence_sqlite_fts5_test.go)。

对于已有的会话和序号，事件表使用 `INSERT OR REPLACE`，而 FTS 表会再插入一条记录。因此重复批次不保证搜索结果幂等，应避免重复写入相同序号。`Open` 没有配置 WAL、busy 重试策略或自定义 synchronous 设置；持久性和争用行为取决于实际驱动及数据库配置。此存储不提供头信息管理和自动未闭合轮次修复。

## 读取持久化状态

使用 `session.FoldAll(events)` 或 `session.FolderFromEvents(events)` 从已加载事件派生状态。这些函数只生成投影，不会自动重建 Agent、重新连接工具或恢复执行。当前 `SessionLog` 没有通用的“加载已有事件”构造函数。把 `event.Data` 再追加到新日志会重新分配时间戳和序号，也不能保留全部事件元数据。

## 验证修改

在仓库根目录运行：

```sh
go test ./pkg/persistence/... ./pkg/session
go test ./tests -run '^Test(Persistence|SQLite)' -count=1
```

[刷新测试](../../pkg/persistence/flush_test.go)覆盖检查点顺序和可恢复的写入前失败；[修复测试](../../pkg/persistence/repair_test.go)覆盖未完成轮次。这些测试不能证明断电持久性、多进程锁定或所有部分写入失败的恢复能力。
