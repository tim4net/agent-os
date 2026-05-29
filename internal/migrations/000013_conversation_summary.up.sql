-- Add summary column to conversations table
ALTER TABLE conversations ADD COLUMN summary TEXT;

-- Update existing "Chat with *" titles to "New conversation"
UPDATE conversations SET title = 'New conversation' WHERE title LIKE 'Chat with %';
