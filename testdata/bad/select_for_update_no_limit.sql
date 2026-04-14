-- expect: select_for_update_no_limit
SELECT id FROM jobs WHERE status = 'pending' FOR UPDATE;
