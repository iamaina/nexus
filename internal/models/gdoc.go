package models

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
)

// Gdoc represents a registered Google Doc source.
type Gdoc struct {
	ID         int64
	Name       string
	DocID      string
	LastSynced *time.Time
	CreatedAt  string
}

// GdocModel handles all database operations for the gdocs table.
type GdocModel struct {
	DB *pgx.Conn
}

// Add registers a new Google Doc. Returns an error if the name already exists.
func (m *GdocModel) Add(ctx context.Context, name, docID string) error {
	_, err := m.DB.Exec(ctx, `
		INSERT INTO gdocs (name, doc_id) VALUES ($1, $2)
	`, name, docID)
	if err != nil {
		return fmt.Errorf("add gdoc %q: %w", name, err)
	}
	return nil
}

// List returns all registered Google Docs ordered by name.
func (m *GdocModel) List(ctx context.Context) ([]Gdoc, error) {
	rows, err := m.DB.Query(ctx, `
		SELECT id, name, doc_id, last_synced,
		       to_char(created_at, 'YYYY-MM-DD HH24:MI') AS created_at
		FROM gdocs ORDER BY name
	`)
	if err != nil {
		return nil, fmt.Errorf("list gdocs: %w", err)
	}
	defer rows.Close()

	var docs []Gdoc
	for rows.Next() {
		var d Gdoc
		if err := rows.Scan(&d.ID, &d.Name, &d.DocID, &d.LastSynced, &d.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan gdoc row: %w", err)
		}
		docs = append(docs, d)
	}
	return docs, rows.Err()
}

// Get returns a single registered Google Doc by name.
// Returns nil, nil if not found.
func (m *GdocModel) Get(ctx context.Context, name string) (*Gdoc, error) {
	var d Gdoc
	err := m.DB.QueryRow(ctx, `
		SELECT id, name, doc_id, last_synced,
		       to_char(created_at, 'YYYY-MM-DD HH24:MI') AS created_at
		FROM gdocs WHERE name = $1
	`, name).Scan(&d.ID, &d.Name, &d.DocID, &d.LastSynced, &d.CreatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get gdoc %q: %w", name, err)
	}
	return &d, nil
}

// Remove deletes a registered Google Doc by name.
func (m *GdocModel) Remove(ctx context.Context, name string) error {
	tag, err := m.DB.Exec(ctx, `DELETE FROM gdocs WHERE name = $1`, name)
	if err != nil {
		return fmt.Errorf("remove gdoc %q: %w", name, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("gdoc %q not found", name)
	}
	return nil
}

// UpdateLastSynced sets last_synced to NOW() for the given doc ID.
func (m *GdocModel) UpdateLastSynced(ctx context.Context, id int64) error {
	_, err := m.DB.Exec(ctx, `UPDATE gdocs SET last_synced = NOW() WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("update last_synced for gdoc %d: %w", id, err)
	}
	return nil
}
