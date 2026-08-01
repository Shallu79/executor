DELETE FROM submissions
 WHERE created_at < $1::timestamptz
