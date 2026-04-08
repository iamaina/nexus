package models

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ContextModel handles database operations for registered live context sources.
type ContextModel struct {
	DB *pgx.Conn
}

// Add upserts a context source by name. If the name already exists the command
// and description are updated in-place, making repeated calls idempotent.
// Returns true if the source was newly created, false if it was updated.
func (m *ContextModel) Add(ctx context.Context, name, command, description string) (created bool, err error) {
	var id int64
	err = m.DB.QueryRow(ctx,
		`INSERT INTO context_sources (name, command, description)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (name) DO UPDATE
		     SET command     = EXCLUDED.command,
		         description = EXCLUDED.description
		 RETURNING (xmax = 0) AS inserted, id`,
		name, command, description,
	).Scan(&created, &id)
	if err != nil {
		return false, fmt.Errorf("add context source %q: %w", name, err)
	}
	return created, nil
}

// List returns all registered context sources ordered by name.
func (m *ContextModel) List(ctx context.Context) ([]ContextSource, error) {
	rows, err := m.DB.Query(ctx,
		`SELECT id, name, command, COALESCE(description,''), TO_CHAR(created_at,'YYYY-MM-DD HH24:MI')
		 FROM context_sources ORDER BY name`,
	)
	if err != nil {
		return nil, fmt.Errorf("list context sources: %w", err)
	}
	defer rows.Close()

	var sources []ContextSource
	for rows.Next() {
		var s ContextSource
		if err := rows.Scan(&s.ID, &s.Name, &s.Command, &s.Description, &s.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan context source: %w", err)
		}
		sources = append(sources, s)
	}
	return sources, rows.Err()
}

// Remove deletes a context source by name. Returns an error if it does not exist.
func (m *ContextModel) Remove(ctx context.Context, name string) error {
	tag, err := m.DB.Exec(ctx,
		`DELETE FROM context_sources WHERE name = $1`, name,
	)
	if err != nil {
		return fmt.Errorf("remove context source %q: %w", name, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("context source %q not found", name)
	}
	return nil
}

// Get returns a single context source by name.
func (m *ContextModel) Get(ctx context.Context, name string) (*ContextSource, error) {
	var s ContextSource
	err := m.DB.QueryRow(ctx,
		`SELECT id, name, command, COALESCE(description,''), TO_CHAR(created_at,'YYYY-MM-DD HH24:MI')
		 FROM context_sources WHERE name = $1`, name,
	).Scan(&s.ID, &s.Name, &s.Command, &s.Description, &s.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("context source %q not found", name)
	}
	if err != nil {
		return nil, fmt.Errorf("get context source %q: %w", name, err)
	}
	return &s, nil
}
