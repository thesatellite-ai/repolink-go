package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Profile is a per-machine profile row (hostname tracking + run_logs ref).
// The authoritative "active profile" lives in ~/.repolink/config.jsonc;
// this table only exists so RunLog can reference which machine did what.
type Profile struct{ ent.Schema }

func (Profile) Annotations() []schema.Annotation {
	return []schema.Annotation{entsql.Annotation{Table: "profiles"}}
}

func (Profile) Fields() []ent.Field {
	return []ent.Field{
		field.String("id").
			DefaultFunc(uuidV7).
			Immutable().
			NotEmpty(),
		field.String("name").
			NotEmpty().
			Comment("mirrors profile key in config.jsonc; not unique across machines"),
		field.String("hostname").
			Comment("os.Hostname() at profile creation time"),
		field.Time("created_at").Default(now).Immutable(),
		field.Time("updated_at").Default(now).UpdateDefault(now),
	}
}

func (Profile) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("name", "hostname").Unique(),
	}
}
