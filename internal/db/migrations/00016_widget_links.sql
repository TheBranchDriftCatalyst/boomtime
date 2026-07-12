-- +goose Up

-- gaka-hsj: public, uuid-scoped widget links (embeddable SVG stats cards).
-- One row per (user, scope); widget kind + range + theme are URL params on the
-- public endpoint, so a single uuid serves every widget kind for that scope.
-- scope_ref: '' for user scope, project name for project scope, space id (as
-- text) for space scope. No FK on scope_ref: it is polymorphic across scope
-- types (Postgres FKs cannot be conditional), and project "renames" in this app
-- are query-time remaps — raw project names never change. Ownership of the
-- scope is validated at mint time by the handler.
CREATE TABLE widget_links (
    link_id uuid UNIQUE NOT NULL DEFAULT uuid_generate_v4(),
    username text NOT NULL REFERENCES users(username),
    scope_type text NOT NULL CHECK (scope_type IN ('user', 'project', 'space')),
    scope_ref text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (username, scope_type, scope_ref)
);

-- +goose Down
DROP TABLE widget_links;
