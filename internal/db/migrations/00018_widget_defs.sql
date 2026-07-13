-- +goose Up

-- gaka-3nu: named/saved custom widget compositions. The gaka-567 builder
-- shipped with the whole composition base64-inlined in the URL as ?spec=,
-- which is fine for one-shot embeds but rotates on every edit and can grow
-- large. widget_defs persists the composition server-side so a user can name
-- it, iterate on it, and get a stable short URL (/widget/svg/:def_id/named)
-- that doesn't change on each edit.
--
-- spec JSONB mirrors widget.Def (layout + panels[] + title). Sibling to
-- widget_links: they solve different problems (widget_links = per-scope
-- share URL with hit tracking + roll; widget_defs = named saved composition).
-- Deliberately no scope columns: v1 is user-scoped only; a future revision
-- can add scope_type/scope_ref to make named defs scope-aware.
CREATE TABLE widget_defs (
    def_id     uuid UNIQUE NOT NULL DEFAULT uuid_generate_v4(),
    username   text NOT NULL REFERENCES users(username),
    name       text NOT NULL,
    spec       jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (username, name)
);

CREATE INDEX widget_defs_username_idx ON widget_defs(username);

-- +goose Down
DROP TABLE widget_defs;
