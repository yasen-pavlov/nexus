package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/muty/nexus/internal/model"
)

const chatCols = `id, user_id, title, default_model, created_at, updated_at`
const chatColsQualified = `c.id, c.user_id, c.title, c.default_model, c.created_at, c.updated_at`
const chatMessageCols = `id, chat_id, role, seq, content, model, citations, evidence, tool_calls, usage, stop_reason, rewritten_query, skipped_retrieval, duration_ms, created_at`

// ChatUpdate carries the fields a PATCH may set. Nil fields are left untouched.
type ChatUpdate struct {
	Title        *string
	DefaultModel *string
}

func scanChat(scan func(dest ...any) error) (*model.Chat, error) {
	var c model.Chat
	err := scan(&c.ID, &c.UserID, &c.Title, &c.DefaultModel, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// CreateChat inserts a new chat. Assigns ID + timestamps if zero.
func (s *Store) CreateChat(ctx context.Context, c *model.Chat) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	now := time.Now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = now
	}
	c.UpdatedAt = now

	_, err := s.pool.Exec(ctx,
		`INSERT INTO chats (`+chatCols+`) VALUES ($1, $2, $3, $4, $5, $6)`,
		c.ID, c.UserID, c.Title, c.DefaultModel, c.CreatedAt, c.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: create chat: %w", err)
	}
	return nil
}

// GetChat returns the chat by id. Returns ErrNotFound when missing.
func (s *Store) GetChat(ctx context.Context, id uuid.UUID) (*model.Chat, error) {
	c, err := scanChat(func(dest ...any) error {
		return s.pool.QueryRow(ctx,
			`SELECT `+chatCols+` FROM chats WHERE id = $1`, id,
		).Scan(dest...)
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("store: get chat: %w", err)
	}
	return c, nil
}

// chatPreviewMaxChars caps the first_message_preview substring at the SQL
// boundary so a multi-MB pasted prompt doesn't fan out across the list
// response. The FE truncates further for the recent-chats card; this is
// just a defensive ceiling.
const chatPreviewMaxChars = 240

// ListChats returns chats owned by userID ordered by updated_at desc, plus
// the total count for pagination. Each entry carries the first user
// message as a preview (joined via LATERAL so it stays a single
// round-trip, even when the chat has no user message yet).
func (s *Store) ListChats(ctx context.Context, userID uuid.UUID, limit, offset int) ([]model.ChatListEntry, int, error) {
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}

	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM chats WHERE user_id = $1`, userID,
	).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("store: list chats count: %w", err)
	}

	rows, err := s.pool.Query(ctx,
		`SELECT `+chatColsQualified+`, COALESCE(LEFT(preview.content, $4), '') AS first_message_preview
		 FROM chats c
		 LEFT JOIN LATERAL (
		     SELECT content FROM chat_messages
		     WHERE chat_id = c.id AND role = 'user'
		     ORDER BY seq ASC LIMIT 1
		 ) AS preview ON true
		 WHERE c.user_id = $1
		 ORDER BY c.updated_at DESC LIMIT $2 OFFSET $3`,
		userID, limit, offset, chatPreviewMaxChars,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("store: list chats: %w", err)
	}
	defer rows.Close()

	chats := []model.ChatListEntry{}
	for rows.Next() {
		var entry model.ChatListEntry
		if err := rows.Scan(
			&entry.ID, &entry.UserID, &entry.Title, &entry.DefaultModel,
			&entry.CreatedAt, &entry.UpdatedAt, &entry.FirstMessagePreview,
		); err != nil {
			return nil, 0, fmt.Errorf("store: scan chat list entry: %w", err)
		}
		chats = append(chats, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("store: list chats rows: %w", err)
	}
	return chats, total, nil
}

// UpdateChat applies a partial update. Returns ErrNotFound if no row matches.
func (s *Store) UpdateChat(ctx context.Context, id uuid.UUID, fields ChatUpdate) error {
	if fields.Title == nil && fields.DefaultModel == nil {
		// No-op update — touch updated_at so callers see a consistent
		// "saved at" even when nothing material changed.
		result, err := s.pool.Exec(ctx,
			`UPDATE chats SET updated_at = $1 WHERE id = $2`, time.Now(), id)
		if err != nil {
			return fmt.Errorf("store: update chat: %w", err)
		}
		if result.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	}

	// Build a single UPDATE so we keep one round-trip and one updated_at write.
	query := `UPDATE chats SET updated_at = $1`
	args := []any{time.Now()}
	idx := 2
	if fields.Title != nil {
		query += fmt.Sprintf(", title = $%d", idx)
		args = append(args, *fields.Title)
		idx++
	}
	if fields.DefaultModel != nil {
		query += fmt.Sprintf(", default_model = $%d", idx)
		args = append(args, *fields.DefaultModel)
		idx++
	}
	query += fmt.Sprintf(" WHERE id = $%d", idx)
	args = append(args, id)

	result, err := s.pool.Exec(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("store: update chat: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteChat removes a chat. ON DELETE CASCADE clears chat_messages.
func (s *Store) DeleteChat(ctx context.Context, id uuid.UUID) error {
	result, err := s.pool.Exec(ctx, `DELETE FROM chats WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("store: delete chat: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanChatMessage(scan func(dest ...any) error) (*model.ChatMessage, error) {
	var m model.ChatMessage
	var modelStr, stopReason, rewrittenQuery *string
	var durationMs *int
	var citationsJSON, evidenceJSON, toolCallsJSON, usageJSON []byte
	err := scan(
		&m.ID, &m.ChatID, &m.Role, &m.Seq, &m.Content,
		&modelStr, &citationsJSON, &evidenceJSON, &toolCallsJSON, &usageJSON, &stopReason,
		&rewrittenQuery, &m.SkippedRetrieval, &durationMs, &m.CreatedAt,
	)
	if err != nil {
		return nil, err
	}
	m.DurationMs = durationMs
	if modelStr != nil {
		m.Model = *modelStr
	}
	if stopReason != nil {
		m.StopReason = *stopReason
	}
	if rewrittenQuery != nil {
		m.RewrittenQuery = *rewrittenQuery
	}
	if len(citationsJSON) > 0 {
		if err := json.Unmarshal(citationsJSON, &m.Citations); err != nil {
			return nil, fmt.Errorf("store: unmarshal citations: %w", err)
		}
	}
	if len(evidenceJSON) > 0 {
		if err := json.Unmarshal(evidenceJSON, &m.Evidence); err != nil {
			return nil, fmt.Errorf("store: unmarshal evidence: %w", err)
		}
	}
	if len(toolCallsJSON) > 0 {
		if err := json.Unmarshal(toolCallsJSON, &m.ToolCalls); err != nil {
			return nil, fmt.Errorf("store: unmarshal tool_calls: %w", err)
		}
	}
	if len(usageJSON) > 0 {
		if err := json.Unmarshal(usageJSON, &m.Usage); err != nil {
			return nil, fmt.Errorf("store: unmarshal usage: %w", err)
		}
	}
	return &m, nil
}

// ListMessages returns all messages in a chat, ordered by seq ascending.
func (s *Store) ListMessages(ctx context.Context, chatID uuid.UUID) ([]model.ChatMessage, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+chatMessageCols+` FROM chat_messages WHERE chat_id = $1 ORDER BY seq ASC`,
		chatID,
	)
	if err != nil {
		return nil, fmt.Errorf("store: list messages: %w", err)
	}
	defer rows.Close()

	msgs := []model.ChatMessage{}
	for rows.Next() {
		m, err := scanChatMessage(rows.Scan)
		if err != nil {
			return nil, fmt.Errorf("store: scan chat message: %w", err)
		}
		msgs = append(msgs, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list messages rows: %w", err)
	}
	return msgs, nil
}

// AppendMessage inserts a new message at the next monotonic seq for its
// chat. Locks the chat row inside a tx so concurrent appends serialise.
// Bumps chats.updated_at as a side effect.
func (s *Store) AppendMessage(ctx context.Context, msg *model.ChatMessage) error {
	if msg.ID == uuid.Nil {
		msg.ID = uuid.New()
	}
	if msg.CreatedAt.IsZero() {
		msg.CreatedAt = time.Now()
	}

	citationsJSON, err := marshalNullable(msg.Citations)
	if err != nil {
		return fmt.Errorf("store: marshal citations: %w", err)
	}
	evidenceJSON, err := marshalNullable(msg.Evidence)
	if err != nil {
		return fmt.Errorf("store: marshal evidence: %w", err)
	}
	toolCallsJSON, err := marshalNullable(msg.ToolCalls)
	if err != nil {
		return fmt.Errorf("store: marshal tool_calls: %w", err)
	}
	usageJSON, err := marshalNullable(msg.Usage)
	if err != nil {
		return fmt.Errorf("store: marshal usage: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("store: begin tx for append message: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	// Lock the chat row to serialise concurrent appends. Without this,
	// two concurrent inserts can compute the same MAX(seq)+1 and one
	// will lose the unique constraint race.
	var dummy uuid.UUID
	if err := tx.QueryRow(ctx,
		`SELECT id FROM chats WHERE id = $1 FOR UPDATE`, msg.ChatID,
	).Scan(&dummy); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("store: lock chat for append: %w", err)
	}

	if err := tx.QueryRow(ctx,
		`SELECT COALESCE(MAX(seq), 0) + 1 FROM chat_messages WHERE chat_id = $1`,
		msg.ChatID,
	).Scan(&msg.Seq); err != nil {
		return fmt.Errorf("store: compute next seq: %w", err)
	}

	_, err = tx.Exec(ctx,
		`INSERT INTO chat_messages (`+chatMessageCols+`) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`,
		msg.ID, msg.ChatID, msg.Role, msg.Seq, msg.Content,
		nullableString(msg.Model), citationsJSON, evidenceJSON, toolCallsJSON, usageJSON,
		nullableString(msg.StopReason), nullableString(msg.RewrittenQuery), msg.SkippedRetrieval,
		msg.DurationMs, msg.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("store: insert chat message: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`UPDATE chats SET updated_at = $1 WHERE id = $2`, msg.CreatedAt, msg.ChatID,
	); err != nil {
		return fmt.Errorf("store: bump chat updated_at: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("store: commit append message: %w", err)
	}
	return nil
}

// marshalNullable returns nil bytes for nil/empty values so the column
// stores JSON NULL — readers special-case nil and skip Unmarshal.
func marshalNullable(v any) ([]byte, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []model.ChatCitation:
		if len(t) == 0 {
			return nil, nil
		}
	case []model.ChatToolCall:
		if len(t) == 0 {
			return nil, nil
		}
	case []model.ChunkPreview:
		if len(t) == 0 {
			return nil, nil
		}
	case *model.ChatUsage:
		if t == nil {
			return nil, nil
		}
	}
	return json.Marshal(v)
}

func nullableString(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
