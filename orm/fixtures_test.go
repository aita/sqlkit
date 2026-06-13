package orm_test

import (
	"github.com/aita/sqlkit"
	"github.com/aita/sqlkit/orm"
	"github.com/aita/sqlkit/sql"
)

// These hand-written bindings mirror the shape codegen emits with relations
// enabled (columns-only struct + table struct embedding it + relation structs
// that embed the target columns). They double as a spec for the generator.

// --- users ---

type UsersColumns struct {
	info      sqlkit.TableInfo
	ID        sqlkit.Column[int64]
	Age       sqlkit.Column[int]
	Name      sqlkit.Column[string]
	CreatedAt sqlkit.Column[string]
}

func newUsersColumns() UsersColumns {
	info := sqlkit.TableInfo{Name: "users"}
	return UsersColumns{
		info:      info,
		ID:        sqlkit.NewColumn[int64](info, "id"),
		Age:       sqlkit.NewColumn[int](info, "age"),
		Name:      sqlkit.NewColumn[string](info, "name"),
		CreatedAt: sqlkit.NewColumn[string](info, "created_at"),
	}
}

func (t UsersColumns) Info() sqlkit.TableInfo             { return t.info }
func (t UsersColumns) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t UsersColumns) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{t.ID, t.Age, t.Name, t.CreatedAt}
}
func (t UsersColumns) PrimaryKey() []sqlkit.AnyColumn { return []sqlkit.AnyColumn{t.ID} }
func (t UsersColumns) Defaulted() []sqlkit.AnyColumn  { return []sqlkit.AnyColumn{t.ID} }

type UsersRels struct {
	Profile UsersProfileRel
	Posts   UsersPostsRel
}

type UsersProfileRel struct{ ProfilesColumns }

func (r UsersProfileRel) Target() sqlkit.Table { return r.ProfilesColumns }
func (r UsersProfileRel) JoinOn() sqlkit.Predicate {
	return sqlkit.Cond(sql.Eq(r.ProfilesColumns.UserID, newUsersColumns().ID))
}
func (r UsersProfileRel) Cardinality() orm.Cardinality { return orm.ToOne }
func (r UsersProfileRel) Name() string                 { return "Profile" }

type UsersPostsRel struct{ PostsColumns }

func (r UsersPostsRel) Target() sqlkit.Table { return r.PostsColumns }
func (r UsersPostsRel) JoinOn() sqlkit.Predicate {
	return sqlkit.Cond(sql.Eq(r.PostsColumns.UserID, newUsersColumns().ID))
}
func (r UsersPostsRel) Cardinality() orm.Cardinality { return orm.ToMany }
func (r UsersPostsRel) Name() string                 { return "Posts" }

type UsersTable struct {
	UsersColumns
	Rels UsersRels
}

func (t UsersTable) Queryer(ex sqlkit.Executor) orm.QuerySet { return orm.NewQuerySet(ex, t) }
func (t UsersTable) Q(ex sqlkit.Executor) orm.QuerySet       { return orm.NewQuerySet(ex, t) }

var Users = UsersTable{
	UsersColumns: newUsersColumns(),
	Rels: UsersRels{
		Profile: UsersProfileRel{ProfilesColumns: newProfilesColumns()},
		Posts:   UsersPostsRel{PostsColumns: newPostsColumns()},
	},
}

// --- profiles (user_id unique -> users has one profile) ---

type ProfilesColumns struct {
	info   sqlkit.TableInfo
	ID     sqlkit.Column[int64]
	UserID sqlkit.Column[int64]
	Bio    sqlkit.Column[string]
	Public sqlkit.Column[bool]
}

func newProfilesColumns() ProfilesColumns {
	info := sqlkit.TableInfo{Name: "profiles"}
	return ProfilesColumns{
		info:   info,
		ID:     sqlkit.NewColumn[int64](info, "id"),
		UserID: sqlkit.NewColumn[int64](info, "user_id"),
		Bio:    sqlkit.NewColumn[string](info, "bio"),
		Public: sqlkit.NewColumn[bool](info, "public"),
	}
}

func (t ProfilesColumns) Info() sqlkit.TableInfo             { return t.info }
func (t ProfilesColumns) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t ProfilesColumns) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{t.ID, t.UserID, t.Bio, t.Public}
}
func (t ProfilesColumns) PrimaryKey() []sqlkit.AnyColumn { return []sqlkit.AnyColumn{t.ID} }
func (t ProfilesColumns) Defaulted() []sqlkit.AnyColumn  { return []sqlkit.AnyColumn{t.ID} }

type ProfilesRels struct {
	User ProfilesUserRel
}

type ProfilesUserRel struct{ UsersColumns }

func (r ProfilesUserRel) Target() sqlkit.Table { return r.UsersColumns }
func (r ProfilesUserRel) JoinOn() sqlkit.Predicate {
	return sqlkit.Cond(sql.Eq(r.UsersColumns.ID, newProfilesColumns().UserID))
}
func (r ProfilesUserRel) Cardinality() orm.Cardinality { return orm.ToOne }
func (r ProfilesUserRel) Name() string                 { return "User" }

type ProfilesTable struct {
	ProfilesColumns
	Rels ProfilesRels
}

var Profiles = ProfilesTable{
	ProfilesColumns: newProfilesColumns(),
	Rels:            ProfilesRels{User: ProfilesUserRel{UsersColumns: newUsersColumns()}},
}

// --- posts (user_id -> users has many posts) ---

type PostsColumns struct {
	info   sqlkit.TableInfo
	ID     sqlkit.Column[int64]
	UserID sqlkit.Column[int64]
	Title  sqlkit.Column[string]
}

func newPostsColumns() PostsColumns {
	info := sqlkit.TableInfo{Name: "posts"}
	return PostsColumns{
		info:   info,
		ID:     sqlkit.NewColumn[int64](info, "id"),
		UserID: sqlkit.NewColumn[int64](info, "user_id"),
		Title:  sqlkit.NewColumn[string](info, "title"),
	}
}

func (t PostsColumns) Info() sqlkit.TableInfo             { return t.info }
func (t PostsColumns) SQLSource() sqlkit.SourceExpression { return t.info.SQLSource() }
func (t PostsColumns) Columns() []sqlkit.AnyColumn {
	return []sqlkit.AnyColumn{t.ID, t.UserID, t.Title}
}
func (t PostsColumns) PrimaryKey() []sqlkit.AnyColumn { return []sqlkit.AnyColumn{t.ID} }
func (t PostsColumns) Defaulted() []sqlkit.AnyColumn  { return []sqlkit.AnyColumn{t.ID} }

type PostsRels struct {
	User PostsUserRel
}

type PostsUserRel struct{ UsersColumns }

func (r PostsUserRel) Target() sqlkit.Table { return r.UsersColumns }
func (r PostsUserRel) JoinOn() sqlkit.Predicate {
	return sqlkit.Cond(sql.Eq(r.UsersColumns.ID, newPostsColumns().UserID))
}
func (r PostsUserRel) Cardinality() orm.Cardinality { return orm.ToOne }
func (r PostsUserRel) Name() string                 { return "User" }

type PostsTable struct {
	PostsColumns
	Rels PostsRels
}

func (t PostsTable) Queryer(ex sqlkit.Executor) orm.QuerySet { return orm.NewQuerySet(ex, t) }
func (t PostsTable) Q(ex sqlkit.Executor) orm.QuerySet       { return orm.NewQuerySet(ex, t) }

var Posts = PostsTable{
	PostsColumns: newPostsColumns(),
	Rels:         PostsRels{User: PostsUserRel{UsersColumns: newUsersColumns()}},
}
