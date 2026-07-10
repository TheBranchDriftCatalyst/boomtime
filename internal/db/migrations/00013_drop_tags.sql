-- +goose Up
-- The tag feature was entirely unused (no data to migrate); Spaces subsume it.
DROP TABLE IF EXISTS project_tags;
DROP TABLE IF EXISTS tags;

-- +goose Down
-- Recreate the (empty) tables so a rollback restores the prior schema shape.
CREATE TABLE IF NOT EXISTS tags (
    id uuid UNIQUE DEFAULT UUID_GENERATE_V4 (),
    name text PRIMARY KEY
);

CREATE TABLE IF NOT EXISTS project_tags (
    project_name text NOT NULL,
    project_owner text NOT NULL,
    tag_id uuid REFERENCES tags (id),

    CONSTRAINT project_tags_pname_powner_fkey FOREIGN KEY (project_owner, project_name) REFERENCES projects (owner, name),
    PRIMARY KEY (project_name, project_owner, tag_id)
);
