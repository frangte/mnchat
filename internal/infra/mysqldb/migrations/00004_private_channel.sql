-- Demo data: a private channel for showing permissions. Carol and Dave can't see it.

-- +goose Up
INSERT INTO channels (id, workspace_id, type, name, created_by) VALUES
    (6, 1, 'private', 'leadership', 1);

INSERT INTO channel_members (channel_id, user_id) VALUES
    (6, 1), (6, 2);

INSERT INTO messages (workspace_id, channel_id, user_id, body) VALUES
    (1, 6, 1, 'Confidential: the reorganization plan goes to the board next week.'),
    (1, 6, 2, 'Understood. Please keep the reorganization details in this channel until the announcement.');

-- +goose Down
DELETE FROM messages WHERE channel_id = 6;
DELETE FROM channel_members WHERE channel_id = 6;
DELETE FROM channels WHERE id = 6;
