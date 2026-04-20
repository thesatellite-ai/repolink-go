// Package schema defines repolink's ent entities.
// Table naming: snake_case, lowercase, plural (or singular for singleton).
// Primary keys: UUID v7 (TEXT) — set via Default func at runtime.
// No DDL foreign keys — see PROBLEM.md "Foreign keys — not used anywhere".
package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"

	"github.com/google/uuid"
)

// RepoMeta is the singleton row identifying a private-repo cross-machine.
// Table: repo_meta (singular — one row per DB).
type RepoMeta struct{ ent.Schema }

func (RepoMeta) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "repo_meta"}}
}

func (RepoMeta) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			DefaultFunc(func() string { return uuidV7() }).
			Immutable().
			NotEmpty(),
		field.String("private_repo_id").
			DefaultFunc(func() string { return uuidV7() }).
			Immutable().
			NotEmpty(),
		field.String("display_name").
			NotEmpty(),
		field.Time("created_at").
			Default(now).
			Immutable(),
	}
}

// uuidV7 wraps google/uuid.NewV7 into a non-erroring helper.
// Ent's DefaultFunc signature requires a function that returns exactly the
// field's Go type, so we swallow the (always-nil) error here.
func uuidV7() string {
	id, err := uuid.NewV7()
	if err != nil {
		// uuid.NewV7 only errors if crypto/rand fails — treat as fatal.
		panic("uuid.NewV7: " + err.Error())
	}
	return id.String()
}
