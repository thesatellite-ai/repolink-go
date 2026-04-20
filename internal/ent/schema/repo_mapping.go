package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// RepoMapping is one symlink intention: a relative source path inside the
// private-repo, bound to a consumer repo (`repo_url`) at a relative target
// location with a chosen link filename.
type RepoMapping struct{ ent.Schema }

func (RepoMapping) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "repo_mappings"}}
}

func (RepoMapping) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			DefaultFunc(uuidV7).
			Immutable().
			NotEmpty(),
		field.String("source_rel").
			NotEmpty().
			Comment("path relative to the private-repo root; e.g. markdown-viewer-chrome-ext"),
		field.String("repo_url").
			NotEmpty().
			Comment("canonical consumer repo URL; e.g. github.com/khanakia/abc"),
		field.String("target_rel").
			Comment("relative dir inside the consumer repo; e.g. research (empty = repo root)"),
		field.String("link_name").
			NotEmpty().
			Comment("filename of the symlink inside target_rel"),
		field.Enum("kind").
			Values("dir", "file").
			Default("dir"),
		field.Enum("state").
			Values("active", "paused", "trashed").
			Default("active"),
		field.String("created_by_email").Optional(),
		field.String("created_by_name").Optional(),
		field.String("updated_by_email").Optional(),
		field.String("updated_by_name").Optional(),
		field.String("notes").Optional(),
		field.Time("created_at").Default(now).Immutable(),
		field.Time("updated_at").Default(now).UpdateDefault(now),
	}
}

func (RepoMapping) Indexes() []ent.Index {
	return []ent.Index{
		// Target uniqueness WITHIN this DB. Cross-DB uniqueness enforced in CLI.
		index.Fields("repo_url", "target_rel", "link_name").Unique(),
		// Lookup for "which consumer repos does this source feed?"
		index.Fields("source_rel", "repo_url"),
	}
}
