INSERT INTO heartbeats
(
    editor,
    plugin,
    platform,
    machine,
    sender,
    user_agent,
    branch,
    category,
    cursorpos,
    dependencies,
    entity,
    is_write,
    language,
    lineno,
    file_lines,
    project,
    ty,
    time_sent,
    ai_input_tokens,
    ai_output_tokens,
    ai_line_changes,
    human_line_changes,
    ai_prompt_length,
    ai_session,
    ai_subscription_plan
)

VALUES ( $1, $2, $3, $4, $5,
         $6, $7, $8, $9, $10,
         $11, $12, $13, CAST($14 AS INT), $15,
         $16, $17, $18,
         $19, $20, $21, $22, $23, $24, $25 )

ON CONFLICT ON CONSTRAINT unique_heartbeats
DO UPDATE SET machine=EXCLUDED.machine RETURNING id;
