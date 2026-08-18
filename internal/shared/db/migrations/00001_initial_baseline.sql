-- +goose Up
-- +goose StatementBegin

-- boomtime schema baseline — squashed from migrations 00001 through 00031
-- as of git commit 68f329b (2026-07-24). Every earlier migration file is
-- deleted from this directory.
--
-- WHY THE SQUASH: 31 sequential migrations accumulated during boomtime's
-- v0.x → v0.5.5 lifetime. Fresh installs (dev workstations, CI test DBs)
-- had to replay every one, several of which were transitional (v26 dual-
-- path hash addition, v27 constraint relaxation, v30 backfill, v31 raw
-- column drop) — none of which needed to persist as history once the raw
-- token cutover completed. Collapsing to a single baseline drops setup
-- time on fresh envs and removes 500+ lines of migration file noise.
--
-- HOW GOOSE HANDLES IT: prod (any DB already at goose_db_version.version_id
-- >= 1) sees this v1 file, notes v1 is already recorded as applied,
-- skips. Fresh DBs (no goose_db_version rows) see v1 as pending, run
-- this baseline once, come up at the same schema shape prod already has.
-- goose_db_version.version_id entries 2..31 remain on existing DBs as
-- orphaned entries with no matching file — cosmetic only, no runtime
-- impact.
--
-- REGENERATE: pg_dump -U postgres --schema-only --no-owner --no-privileges
--   --no-comments --exclude-table=goose_db_version boomtime > dump.sql
-- Then strip pg_dump preamble/postamble, wrap in the goose Up/Down markers.

CREATE EXTENSION IF NOT EXISTS pg_trgm WITH SCHEMA public;



CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public;



CREATE EXTENSION IF NOT EXISTS "uuid-ossp" WITH SCHEMA public;


SET default_tablespace = '';

SET default_table_access_method = heap;


CREATE TABLE public.auth_tokens (
    owner text,
    token_expiry timestamp without time zone,
    last_usage timestamp without time zone,
    token_name text,
    token_description text,
    hashed_token bytea NOT NULL
);



CREATE TABLE public.badges (
    link_id uuid DEFAULT public.uuid_generate_v4(),
    username text NOT NULL,
    project text NOT NULL
);



CREATE TABLE public.curation_rules (
    id integer NOT NULL,
    sender text NOT NULL,
    axis text NOT NULL,
    action text NOT NULL,
    match_value text NOT NULL,
    new_value text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    match_type text DEFAULT 'exact'::text NOT NULL
);



CREATE SEQUENCE public.curation_rules_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.curation_rules_id_seq OWNED BY public.curation_rules.id;



CREATE TABLE public.hb_rollup_daily (
    sender text NOT NULL,
    day date NOT NULL,
    project text NOT NULL,
    language text NOT NULL,
    editor text NOT NULL,
    platform text NOT NULL,
    machine text NOT NULL,
    category text NOT NULL,
    plugin text NOT NULL,
    branch text NOT NULL,
    total_seconds bigint NOT NULL
);



CREATE TABLE public.health_rollup_daily (
    owner text NOT NULL,
    day date NOT NULL,
    kind text NOT NULL,
    total_qty real,
    avg_qty real,
    min_qty real,
    max_qty real,
    sample_count integer NOT NULL
);



CREATE TABLE public.health_samples (
    id bigint NOT NULL,
    owner text NOT NULL,
    kind text NOT NULL,
    unit text NOT NULL,
    qty real,
    q_min real,
    q_avg real,
    q_max real,
    ts_start timestamp with time zone NOT NULL,
    ts_end timestamp with time zone,
    meta jsonb,
    workout_id bigint
);



CREATE SEQUENCE public.health_samples_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.health_samples_id_seq OWNED BY public.health_samples.id;



CREATE TABLE public.heartbeats (
    id integer NOT NULL,
    editor text,
    plugin text,
    platform text,
    machine text,
    sender text,
    user_agent text,
    branch text,
    category text,
    cursorpos text,
    dependencies text[],
    entity text NOT NULL,
    is_write boolean,
    language text,
    lineno integer,
    file_lines integer,
    project text,
    ty text NOT NULL,
    time_sent timestamp without time zone NOT NULL,
    gap_seconds integer,
    ai_input_tokens integer,
    ai_output_tokens integer,
    ai_line_changes integer,
    human_line_changes integer,
    ai_prompt_length integer,
    ai_session text,
    ai_subscription_plan text,
    workout_kind text,
    workout_duration_s integer,
    workout_kcal real,
    workout_avg_hr integer,
    workout_distance_m real
);



CREATE SEQUENCE public.heartbeats_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.heartbeats_id_seq OWNED BY public.heartbeats.id;



CREATE TABLE public.import_job_logs (
    id bigint NOT NULL,
    job_id integer NOT NULL,
    ts timestamp with time zone DEFAULT now() NOT NULL,
    level text NOT NULL,
    message text NOT NULL
);



CREATE SEQUENCE public.import_job_logs_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.import_job_logs_id_seq OWNED BY public.import_job_logs.id;



CREATE TABLE public.import_jobs (
    id integer NOT NULL,
    value jsonb NOT NULL,
    state text DEFAULT 'queued'::text NOT NULL,
    error text,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    owner text,
    start_date timestamp with time zone,
    end_date timestamp with time zone,
    total_days integer,
    processed_days integer DEFAULT 0 NOT NULL,
    imported_count bigint DEFAULT 0 NOT NULL,
    current_day date,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    drift jsonb
);



CREATE SEQUENCE public.import_jobs_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.import_jobs_id_seq OWNED BY public.import_jobs.id;



CREATE TABLE public.projects (
    name text NOT NULL,
    description text,
    owner text NOT NULL,
    dependencies text[],
    repository text
);



CREATE TABLE public.refresh_tokens (
    owner text,
    token_expiry timestamp without time zone,
    hashed_refresh_token bytea NOT NULL
);



CREATE TABLE public.space_rules (
    id integer NOT NULL,
    space_id integer NOT NULL,
    axis text NOT NULL,
    match_value text NOT NULL,
    match_type text DEFAULT 'exact'::text NOT NULL
);



CREATE SEQUENCE public.space_rules_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.space_rules_id_seq OWNED BY public.space_rules.id;



CREATE TABLE public.spaces (
    id integer NOT NULL,
    owner text NOT NULL,
    name text NOT NULL,
    "position" integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);



CREATE SEQUENCE public.spaces_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.spaces_id_seq OWNED BY public.spaces.id;



CREATE TABLE public.users (
    username text NOT NULL,
    hashed_password bytea NOT NULL,
    salt_used bytea NOT NULL,
    encrypted_wakatime_key bytea,
    wakatime_key_status text,
    wakatime_key_checked_at timestamp with time zone,
    public_profile_enabled boolean DEFAULT false NOT NULL,
    public_slug text,
    argon_version integer DEFAULT 1 NOT NULL
);



CREATE TABLE public.widget_defs (
    def_id uuid DEFAULT public.uuid_generate_v4() NOT NULL,
    username text NOT NULL,
    name text NOT NULL,
    spec jsonb NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);



CREATE TABLE public.widget_links (
    link_id uuid DEFAULT public.uuid_generate_v4() NOT NULL,
    username text NOT NULL,
    scope_type text NOT NULL,
    scope_ref text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_used_at timestamp with time zone,
    origins jsonb DEFAULT '[]'::jsonb NOT NULL,
    CONSTRAINT widget_links_scope_type_check CHECK ((scope_type = ANY (ARRAY['user'::text, 'project'::text, 'space'::text])))
);



CREATE TABLE public.workout_details (
    heartbeat_id bigint NOT NULL,
    source_uuid text NOT NULL,
    hr_series jsonb,
    route jsonb
);



ALTER TABLE ONLY public.curation_rules ALTER COLUMN id SET DEFAULT nextval('public.curation_rules_id_seq'::regclass);



ALTER TABLE ONLY public.health_samples ALTER COLUMN id SET DEFAULT nextval('public.health_samples_id_seq'::regclass);



ALTER TABLE ONLY public.heartbeats ALTER COLUMN id SET DEFAULT nextval('public.heartbeats_id_seq'::regclass);



ALTER TABLE ONLY public.import_job_logs ALTER COLUMN id SET DEFAULT nextval('public.import_job_logs_id_seq'::regclass);



ALTER TABLE ONLY public.import_jobs ALTER COLUMN id SET DEFAULT nextval('public.import_jobs_id_seq'::regclass);



ALTER TABLE ONLY public.space_rules ALTER COLUMN id SET DEFAULT nextval('public.space_rules_id_seq'::regclass);



ALTER TABLE ONLY public.spaces ALTER COLUMN id SET DEFAULT nextval('public.spaces_id_seq'::regclass);



ALTER TABLE ONLY public.badges
    ADD CONSTRAINT badges_link_id_key UNIQUE (link_id);



ALTER TABLE ONLY public.badges
    ADD CONSTRAINT badges_pkey PRIMARY KEY (username, project);



ALTER TABLE ONLY public.curation_rules
    ADD CONSTRAINT curation_rules_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.hb_rollup_daily
    ADD CONSTRAINT hb_rollup_daily_pkey PRIMARY KEY (sender, day, project, language, editor, platform, machine, category, plugin, branch);



ALTER TABLE ONLY public.health_rollup_daily
    ADD CONSTRAINT health_rollup_daily_pkey PRIMARY KEY (owner, day, kind);



ALTER TABLE ONLY public.health_samples
    ADD CONSTRAINT health_samples_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.heartbeats
    ADD CONSTRAINT heartbeats_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.import_job_logs
    ADD CONSTRAINT import_job_logs_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.import_jobs
    ADD CONSTRAINT import_jobs_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_pkey PRIMARY KEY (name, owner);



ALTER TABLE ONLY public.space_rules
    ADD CONSTRAINT space_rules_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.spaces
    ADD CONSTRAINT spaces_owner_name_key UNIQUE (owner, name);



ALTER TABLE ONLY public.spaces
    ADD CONSTRAINT spaces_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.heartbeats
    ADD CONSTRAINT unique_heartbeats UNIQUE (entity, sender, time_sent);



ALTER TABLE ONLY public.users
    ADD CONSTRAINT users_pkey PRIMARY KEY (username);



ALTER TABLE ONLY public.widget_defs
    ADD CONSTRAINT widget_defs_def_id_key UNIQUE (def_id);



ALTER TABLE ONLY public.widget_defs
    ADD CONSTRAINT widget_defs_pkey PRIMARY KEY (username, name);



ALTER TABLE ONLY public.widget_links
    ADD CONSTRAINT widget_links_link_id_key UNIQUE (link_id);



ALTER TABLE ONLY public.widget_links
    ADD CONSTRAINT widget_links_pkey PRIMARY KEY (username, scope_type, scope_ref);



ALTER TABLE ONLY public.workout_details
    ADD CONSTRAINT workout_details_pkey PRIMARY KEY (heartbeat_id);



CREATE UNIQUE INDEX auth_tokens_hashed_token_key ON public.auth_tokens USING btree (hashed_token) WHERE (hashed_token IS NOT NULL);



CREATE INDEX curation_rules_sender_action_idx ON public.curation_rules USING btree (sender, action);



CREATE UNIQUE INDEX curation_rules_unique_idx ON public.curation_rules USING btree (sender, axis, action, match_type, match_value);



CREATE INDEX datetime_idx ON public.heartbeats USING btree (time_sent);



CREATE INDEX hb_rollup_daily_sender_day_idx ON public.hb_rollup_daily USING btree (sender, day);



CREATE INDEX heartbeats_branch_trgm_idx ON public.heartbeats USING gin (branch public.gin_trgm_ops);



CREATE INDEX heartbeats_entity_trgm_idx ON public.heartbeats USING gin (entity public.gin_trgm_ops);



CREATE INDEX heartbeats_project_pattern_idx ON public.heartbeats USING btree (project text_pattern_ops);



CREATE INDEX heartbeats_project_trgm_idx ON public.heartbeats USING gin (project public.gin_trgm_ops);



CREATE INDEX heartbeats_sender_time_idx ON public.heartbeats USING btree (sender, time_sent);



CREATE UNIQUE INDEX idx_health_samples_dedupe ON public.health_samples USING btree (owner, kind, ts_start, COALESCE(ts_end, ts_start));



CREATE INDEX idx_health_samples_owner_kind_ts ON public.health_samples USING btree (owner, kind, ts_start);



CREATE INDEX idx_health_samples_workout ON public.health_samples USING btree (workout_id) WHERE (workout_id IS NOT NULL);



CREATE UNIQUE INDEX idx_workout_details_source_uuid ON public.workout_details USING btree (source_uuid);



CREATE INDEX import_job_logs_job_id_id_idx ON public.import_job_logs USING btree (job_id, id);



CREATE INDEX import_jobs_owner_idx ON public.import_jobs USING btree (owner);



CREATE INDEX import_jobs_state_idx ON public.import_jobs USING btree (state);



CREATE UNIQUE INDEX refresh_tokens_hashed_refresh_token_key ON public.refresh_tokens USING btree (hashed_refresh_token) WHERE (hashed_refresh_token IS NOT NULL);



CREATE INDEX space_rules_space_id_idx ON public.space_rules USING btree (space_id);



CREATE UNIQUE INDEX users_public_slug_key ON public.users USING btree (public_slug) WHERE (public_slug IS NOT NULL);



CREATE INDEX widget_defs_username_idx ON public.widget_defs USING btree (username);



ALTER TABLE ONLY public.auth_tokens
    ADD CONSTRAINT auth_tokens_owner_fkey FOREIGN KEY (owner) REFERENCES public.users(username);



ALTER TABLE ONLY public.badges
    ADD CONSTRAINT badges_username_fkey FOREIGN KEY (username) REFERENCES public.users(username);



ALTER TABLE ONLY public.badges
    ADD CONSTRAINT badges_username_project_fkey FOREIGN KEY (username, project) REFERENCES public.projects(owner, name) ON UPDATE CASCADE;



ALTER TABLE ONLY public.health_samples
    ADD CONSTRAINT health_samples_owner_fkey FOREIGN KEY (owner) REFERENCES public.users(username);



ALTER TABLE ONLY public.health_samples
    ADD CONSTRAINT health_samples_workout_id_fkey FOREIGN KEY (workout_id) REFERENCES public.heartbeats(id) ON DELETE CASCADE;



ALTER TABLE ONLY public.heartbeats
    ADD CONSTRAINT heartbeats_sender_fkey FOREIGN KEY (sender) REFERENCES public.users(username);



ALTER TABLE ONLY public.heartbeats
    ADD CONSTRAINT heartbeats_sender_project_fkey FOREIGN KEY (sender, project) REFERENCES public.projects(owner, name) ON UPDATE CASCADE;



ALTER TABLE ONLY public.import_job_logs
    ADD CONSTRAINT import_job_logs_job_id_fkey FOREIGN KEY (job_id) REFERENCES public.import_jobs(id) ON DELETE CASCADE;



ALTER TABLE ONLY public.projects
    ADD CONSTRAINT projects_owner_fkey FOREIGN KEY (owner) REFERENCES public.users(username);



ALTER TABLE ONLY public.refresh_tokens
    ADD CONSTRAINT refresh_tokens_owner_fkey FOREIGN KEY (owner) REFERENCES public.users(username);



ALTER TABLE ONLY public.space_rules
    ADD CONSTRAINT space_rules_space_id_fkey FOREIGN KEY (space_id) REFERENCES public.spaces(id) ON DELETE CASCADE;



ALTER TABLE ONLY public.widget_defs
    ADD CONSTRAINT widget_defs_username_fkey FOREIGN KEY (username) REFERENCES public.users(username);



ALTER TABLE ONLY public.widget_links
    ADD CONSTRAINT widget_links_username_fkey FOREIGN KEY (username) REFERENCES public.users(username);



ALTER TABLE ONLY public.workout_details
    ADD CONSTRAINT workout_details_heartbeat_id_fkey FOREIGN KEY (heartbeat_id) REFERENCES public.heartbeats(id) ON DELETE CASCADE;




-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
-- Rolling back the baseline means "return to an empty schema" — done by
-- dropping every table. Not implemented: rollback across a squash boundary
-- is a wipe-and-restore-from-backup scenario, not a goose down. Explicitly
-- unsupported.
-- +goose StatementEnd
