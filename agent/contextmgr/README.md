# Context manager

`contextmgr.Manager` owns the conversation state machine. Storage packages own persistence only.

```text
React Agent
    |
    v
contextmgr.Manager
    |
    v
contextmgr.Store  ->  RAM | files | SQLite | MySQL
```

This split keeps steering, turn commits, run settlement, and fork behavior identical across all backends. Applications normally use a backend constructor, which returns a ready `*contextmgr.Manager`:

```go
manager := ram.NewRAMContextManager()
manager := file.NewFileContextManager("data/conversations")
manager, err := sqlite.NewSQLiteContextManager("data/context.sqlite")
manager, err := mysql.NewMysqlContextManager(host, port, user, password, database)
```

## Manager workflows

- `Create` creates a conversation with its initial committed messages. `Load` returns committed history and distinguishes an unknown ID with `ErrContextNotFound`.
- `Append` adds committed messages. `Replace` replaces committed history while retaining the pending inbox and immutable run snapshots; the React Agent uses it after context compression.
- `Enqueue` adds user messages to the pending inbox. `CommitTurn` atomically appends a complete non-final assistant/tool turn, applies all pending messages after it, and clears the inbox.
- `SettleRun` saves one immutable terminal snapshot. A completed settlement appends the final assistant answer, clears pending messages, and saves the snapshot in one compare-and-swap. Interrupted, canceled, and failed settlements save the snapshot while preserving pending messages.
- `Fork` creates a new context from a settled run. It copies history through the selected run, excludes pending messages, and carries inherited snapshots so a branch can be forked again at an earlier point.
- `Delete` removes all state for a context.

`SettleRun` is idempotent by `RunSignature`. Only the current retained run can settle; `ErrRunNotCurrent`, `ErrRunNotFound`, and `ErrRunNotSettled` distinguish invalid terminal and fork requests.

## Store contract

Custom backends implement four methods:

```go
type Store interface {
	Create(context.Context, *State) (common.ContextUID, error)
	Load(context.Context, common.ContextUID) (*State, error)
	CompareAndSwap(context.Context, common.ContextUID, uint64, *State) error
	Delete(context.Context, common.ContextUID) error
}
```

`State` is the complete persistence unit: revision, committed messages, pending messages, and run snapshots. A conforming Store must:

1. Generate a new `ContextUID` and persist revision `1` in `Create`.
2. Isolate nested message values at the Store boundary so caller mutation cannot change persisted state.
3. Return `ErrContextNotFound` from `Load` and `CompareAndSwap` for an unknown ID.
4. In `CompareAndSwap`, replace the complete state only when the persisted revision equals `expectedRevision`, store revision `expectedRevision + 1`, and otherwise return `ErrRevisionConflict` without a partial write.
5. Make `Delete` idempotent.

Manager retries revision conflicts up to a bounded limit. A Store should return context cancellation and backend errors unchanged or wrapped so callers can inspect them with `errors.Is`.

## Persistence formats

The File Store writes one versioned JSON state file per context through a temporary file and atomic rename. Files written before revisions were introduced remain version `1` and load as revision `1`.

SQLite and MySQL keep `revision` and `state_payload` on `goat_context_conversations`. Constructors run schema migration automatically. For v0.2 databases, an empty state payload is reconstructed from the legacy message, pending-message, and run-snapshot tables. The next successful compare-and-swap writes the complete new payload; no offline data migration is required. `Delete` also clears those legacy rows.

Use RAM for tests and short-lived processes, files for simple single-process local persistence, SQLite for durable single-node applications, and MySQL when multiple processes share a context store.
