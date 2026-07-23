-- +goose Up

-- gaka-awh.6 (Bravo MEDIUM): every users row was hashed under the original
-- Argon2id params (time=1, memory=64 MiB, parallelism=4). OWASP ASVS L1 2025
-- floor is time=2, parallelism=1 (=1 keeps CPU-cache contention working
-- against GPU crackers; time=1 is below the floor).
--
-- We CAN'T force existing users to re-enter their password to migrate hashes,
-- so we tag each row with the argon version that produced its hashed_password
-- and let handler.Login transparently re-hash to the current generation on
-- the next successful auth (see internal/db/auth.go:UpgradeArgonVersion).
--
-- Every EXISTING row is a legacy hash → default 1. New INSERTs in Go code
-- explicitly pass argon_version = 2 (see internal/db/auth.go:InsertUser),
-- and the transparent-rehash path bumps a row from 1 to 2 on the same UPDATE
-- that overwrites hashed_password + salt_used. We deliberately DO NOT flip
-- the column default to 2 here — the schema default stays 1 so a naked
-- `INSERT INTO users(username, hashed_password, salt_used) VALUES (...)`
-- from a mis-updated code path fails loud (that INSERT will produce a
-- v1-tagged row, which is caught by the version tracking test suite).

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS argon_version INT NOT NULL DEFAULT 1;

-- +goose Down

ALTER TABLE users
    DROP COLUMN IF EXISTS argon_version;
