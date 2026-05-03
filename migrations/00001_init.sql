-- +goose Up
CREATE TABLE tasks (
    request_id  TEXT PRIMARY KEY,

    user_id     TEXT NOT NULL,
    driver_type TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK(kind IN ('upsert','remove')),
    received_at INTEGER NOT NULL,

    done_at     INTEGER,
    failed_at   INTEGER,
    last_error  TEXT
);

CREATE INDEX idx_tasks_pending ON tasks(received_at) WHERE done_at IS NULL AND failed_at IS NULL;

CREATE TABLE users (
    user_id     TEXT NOT NULL,
    driver_type TEXT NOT NULL,
    created_at  INTEGER NOT NULL,
    updated_at  INTEGER NOT NULL,
    PRIMARY KEY (user_id, driver_type)
);

-- +goose Down
DROP TABLE users;
DROP TABLE tasks;