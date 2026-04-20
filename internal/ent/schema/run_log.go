package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
)

// RunLog captures one command invocation's outcome — who did what, when,
// and whether it succeeded. Referenced IDs are plain UUID columns (no FK).
type RunLog struct{ ent.Schema }

func (RunLog) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "run_logs"}}
}

func (RunLog) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			DefaultFunc(uuidV7).
			Immutable().
			NotEmpty(),
		field.String("profile_id").
			Comment("plain UUID column — no DDL FK; resolved via ent in Go"),
		field.String("mapping_id").
			Optional().
			Nillable().
			Comment("plain UUID column, nullable; NULL on profile-scope ops (setup, config)"),
		field.Enum("op").
			Values("setup", "sync", "unsync", "link", "unlink", "pause", "resume",
				"cleanup", "purge", "map_mv", "verify", "meta", "config"),
		field.Enum("result").
			Values("ok", "error").
			Default("ok"),
		field.String("user_email").Optional(),
		field.String("user_name").Optional(),
		field.String("message").Optional(),
		field.Time("created_at").Default(now).Immutable(),
	}
}
