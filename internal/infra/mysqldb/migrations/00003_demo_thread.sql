-- Demo data: a thread under Alice's welcome message in #general.

-- +goose Up
SET @root = (SELECT id FROM messages WHERE channel_id = 1 AND body LIKE 'Welcome to Acme HQ!%' LIMIT 1);

INSERT INTO messages (workspace_id, channel_id, thread_root_id, user_id, body) VALUES
    (1, 1, @root, 2, 'Should we pin the onboarding doc here?'),
    (1, 1, @root, 1, 'Good idea, I will add it today.'),
    (1, 1, @root, 3, 'Thanks! That will help the new hires.');

UPDATE messages SET reply_count = 3 WHERE id = @root;

-- +goose Down
SET @root = (SELECT id FROM messages WHERE channel_id = 1 AND body LIKE 'Welcome to Acme HQ!%' LIMIT 1);

DELETE FROM messages WHERE thread_root_id = @root;
UPDATE messages SET reply_count = 0 WHERE id = @root;
