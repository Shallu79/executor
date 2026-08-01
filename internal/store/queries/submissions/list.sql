SELECT id, user_id, language, code, stdin, status, exit_code, stdout, stderr, time_ms, created_at
  FROM submissions
 WHERE user_id = $1
 ORDER BY created_at DESC
 LIMIT $2
