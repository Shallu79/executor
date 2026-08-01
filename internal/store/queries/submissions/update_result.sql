UPDATE submissions
   SET status = $1, exit_code = $2, stdout = $3, stderr = $4, time_ms = $5
 WHERE id = $6
