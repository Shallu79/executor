SELECT id, user_id, language, code, stdin, status, exit_code, stdout, stderr, time_ms, created_at
  FROM submissions
 WHERE id = $1 AND user_id = $2
