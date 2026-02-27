\set ON_ERROR_STOP on
SET search_path = public;
SET TIME ZONE 'UTC';

DROP TABLE IF EXISTS tmp_case_results;
CREATE TEMP TABLE tmp_case_results (
  case_id TEXT PRIMARY KEY,
  pass BOOLEAN NOT NULL,
  actual_result TEXT NOT NULL,
  sqlstate TEXT,
  error_key TEXT,
  constraint_name TEXT,
  note TEXT
);

DROP TABLE IF EXISTS tmp_side_effect_matrix;
CREATE TEMP TABLE tmp_side_effect_matrix (
  case_id TEXT NOT NULL,
  table_name TEXT NOT NULL,
  insert_delta INTEGER NOT NULL,
  update_delta INTEGER NOT NULL,
  delete_delta INTEGER NOT NULL,
  PRIMARY KEY (case_id, table_name)
);

CREATE OR REPLACE FUNCTION _extract_error_key(p_msg TEXT)
RETURNS TEXT AS $$
BEGIN
  RETURN substring(p_msg from '\[error_key=([^\]]+)\]');
END;
$$ LANGUAGE plpgsql;

CREATE OR REPLACE FUNCTION _reset_base_data()
RETURNS VOID AS $$
BEGIN
  DELETE FROM web_read_bindings;
  DELETE FROM sync_commits;
  DELETE FROM security_attempt_windows;
  DELETE FROM migration_requests;
  DELETE FROM month_summaries;
  DELETE FROM day_summaries;
  DELETE FROM leave_records;
  DELETE FROM punch_records;
  DELETE FROM devices;
  DELETE FROM users;

  INSERT INTO users(user_id, pairing_code, recovery_code_hash, writer_device_id, writer_epoch)
  VALUES
    ('00000000-0000-0000-0000-000000000001', '12345678', 'hash_u1', NULL, 1),
    ('00000000-0000-0000-0000-000000000002', '87654321', 'hash_u2', NULL, 1);

  INSERT INTO devices(device_id, user_id, device_name, status)
  VALUES
    ('00000000-0000-0000-0000-000000000101', '00000000-0000-0000-0000-000000000001', 'writer', 'ACTIVE'),
    ('00000000-0000-0000-0000-000000000102', '00000000-0000-0000-0000-000000000001', 'target', 'ACTIVE'),
    ('00000000-0000-0000-0000-000000000103', '00000000-0000-0000-0000-000000000001', 'spare', 'ACTIVE'),
    ('00000000-0000-0000-0000-000000000201', '00000000-0000-0000-0000-000000000002', 'u2writer', 'ACTIVE');

  UPDATE users
     SET writer_device_id = '00000000-0000-0000-0000-000000000101', writer_epoch = 1
   WHERE user_id = '00000000-0000-0000-0000-000000000001';

  UPDATE users
     SET writer_device_id = '00000000-0000-0000-0000-000000000201', writer_epoch = 1
   WHERE user_id = '00000000-0000-0000-0000-000000000002';
END;
$$ LANGUAGE plpgsql;

-- DB-C01
DO $$
DECLARE
  c_punch INT;
  c_leave INT;
  c_day INT;
  c_month INT;
  c_sync INT;
BEGIN
  PERFORM _reset_base_data();

  INSERT INTO punch_records(id, user_id, local_date, type, at_utc, timezone_id, minute_of_day, source, version)
  VALUES
    ('10000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', DATE '2026-02-12', 'START', '2026-02-12 01:10:00+00', 'UTC', 70, 'MANUAL', 1),
    ('10000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', DATE '2026-02-12', 'END',   '2026-02-12 10:10:00+00', 'UTC', 610, 'MANUAL', 1);

  INSERT INTO leave_records(id, user_id, local_date, leave_type, version)
  VALUES ('20000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', DATE '2026-02-13', 'AM', 1);

  INSERT INTO day_summaries(id, user_id, local_date, start_at_utc, end_at_utc, is_leave_day, leave_type, is_late, work_minutes, adjust_minutes, status, version)
  VALUES ('30000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', DATE '2026-02-12', '2026-02-12 01:10:00+00', '2026-02-12 10:10:00+00', FALSE, NULL, FALSE, 540, 0, 'COMPUTED', 1);

  INSERT INTO month_summaries(id, user_id, month_start, work_minutes_total, adjust_minutes_balance, version)
  VALUES ('40000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', DATE '2026-02-01', 540, 0, 1);

  INSERT INTO sync_commits(id, user_id, device_id, writer_epoch, sync_id, payload_hash, status)
  VALUES ('50000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000101', 1, '60000000-0000-0000-0000-000000000001', 'hash-c01', 'APPLIED');

  SELECT count(*) INTO c_punch FROM punch_records WHERE user_id='00000000-0000-0000-0000-000000000001' AND deleted_at IS NULL;
  SELECT count(*) INTO c_leave FROM leave_records WHERE user_id='00000000-0000-0000-0000-000000000001' AND deleted_at IS NULL;
  SELECT count(*) INTO c_day FROM day_summaries WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO c_month FROM month_summaries WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO c_sync FROM sync_commits WHERE user_id='00000000-0000-0000-0000-000000000001';

  INSERT INTO tmp_case_results VALUES (
    'DB-C01',
    (c_punch = 2 AND c_leave = 1 AND c_day = 1 AND c_month = 1 AND c_sync = 1),
    format('counts punch=%s, leave=%s, day=%s, month=%s, sync=%s', c_punch, c_leave, c_day, c_month, c_sync),
    NULL, NULL, NULL,
    'atomic write across 5 object groups success'
  );
END $$;

-- DB-C02
DO $$
DECLARE v_status migration_status;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO migration_requests(id,user_id,from_device_id,to_device_id,mode,status,recovery_code_verified,expires_at)
  VALUES ('70000000-0000-0000-0000-000000000002','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','NORMAL','PENDING',FALSE, now() + interval '30 minutes');
  UPDATE migration_requests SET status='CONFIRMED' WHERE id='70000000-0000-0000-0000-000000000002';
  UPDATE migration_requests SET status='COMPLETED' WHERE id='70000000-0000-0000-0000-000000000002';
  SELECT status INTO v_status FROM migration_requests WHERE id='70000000-0000-0000-0000-000000000002';
  INSERT INTO tmp_case_results VALUES ('DB-C02', v_status='COMPLETED', format('final status=%s', v_status), NULL, NULL, NULL, NULL);
END $$;

-- DB-C03
DO $$
DECLARE v_status migration_status;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO migration_requests(id,user_id,from_device_id,to_device_id,mode,status,recovery_code_verified,expires_at)
  VALUES ('70000000-0000-0000-0000-000000000003','00000000-0000-0000-0000-000000000001',NULL,'00000000-0000-0000-0000-000000000102','FORCED','PENDING',TRUE, now() + interval '30 minutes');
  UPDATE migration_requests SET status='COMPLETED' WHERE id='70000000-0000-0000-0000-000000000003';
  SELECT status INTO v_status FROM migration_requests WHERE id='70000000-0000-0000-0000-000000000003';
  INSERT INTO tmp_case_results VALUES ('DB-C03', v_status='COMPLETED', format('final status=%s', v_status), NULL, NULL, NULL, NULL);
END $$;

-- DB-C04
DO $$
DECLARE v_writer UUID;
DECLARE v_epoch BIGINT;
DECLARE v_old_status device_status;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO migration_requests(id,user_id,from_device_id,to_device_id,mode,status,recovery_code_verified,expires_at)
  VALUES ('70000000-0000-0000-0000-000000000004','00000000-0000-0000-0000-000000000001',NULL,'00000000-0000-0000-0000-000000000102','FORCED','PENDING',TRUE, now() + interval '30 minutes');
  UPDATE migration_requests SET status='COMPLETED' WHERE id='70000000-0000-0000-0000-000000000004';

  UPDATE users
     SET writer_device_id='00000000-0000-0000-0000-000000000102', writer_epoch=writer_epoch+1
   WHERE user_id='00000000-0000-0000-0000-000000000001';
  UPDATE devices
     SET status='REVOKED', revoked_at=now()
   WHERE user_id='00000000-0000-0000-0000-000000000001' AND device_id='00000000-0000-0000-0000-000000000101';

  SELECT writer_device_id, writer_epoch INTO v_writer, v_epoch FROM users WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT status INTO v_old_status FROM devices WHERE device_id='00000000-0000-0000-0000-000000000101';

  INSERT INTO tmp_case_results VALUES (
    'DB-C04',
    (v_writer='00000000-0000-0000-0000-000000000102'::uuid AND v_epoch=2 AND v_old_status='REVOKED'),
    format('writer_device_id=%s, writer_epoch=%s, old_device_status=%s', v_writer, v_epoch, v_old_status),
    NULL,NULL,NULL,NULL
  );
END $$;

-- DB-C05
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_key TEXT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO migration_requests(id,user_id,from_device_id,to_device_id,mode,status,recovery_code_verified,expires_at)
  VALUES ('70000000-0000-0000-0000-000000000005','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','NORMAL','PENDING',FALSE, now() + interval '30 minutes');
  BEGIN
    UPDATE migration_requests SET status='COMPLETED' WHERE id='70000000-0000-0000-0000-000000000005';
    INSERT INTO tmp_case_results VALUES ('DB-C05', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS v_state = RETURNED_SQLSTATE, v_msg = MESSAGE_TEXT;
    v_key := _extract_error_key(v_msg);
    INSERT INTO tmp_case_results VALUES ('DB-C05', (v_state='P0001' AND v_key='MIGRATION_TRANSITION_INVALID'), v_msg, v_state, v_key, NULL, NULL);
  END;
END $$;

-- DB-C06
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_key TEXT;
BEGIN
  PERFORM _reset_base_data();
  BEGIN
    INSERT INTO migration_requests(id,user_id,from_device_id,to_device_id,mode,status,recovery_code_verified,expires_at)
    VALUES ('70000000-0000-0000-0000-000000000006','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000103','00000000-0000-0000-0000-000000000102','NORMAL','PENDING',FALSE, now() + interval '30 minutes');
    INSERT INTO tmp_case_results VALUES ('DB-C06', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS v_state = RETURNED_SQLSTATE, v_msg = MESSAGE_TEXT;
    v_key := _extract_error_key(v_msg);
    INSERT INTO tmp_case_results VALUES ('DB-C06', (v_state='P0001' AND v_key='MIGRATION_SOURCE_MISMATCH'), v_msg, v_state, v_key, NULL, NULL);
  END;
END $$;

-- DB-C07
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_key TEXT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO migration_requests(id,user_id,from_device_id,to_device_id,mode,status,recovery_code_verified,expires_at)
  VALUES ('70000000-0000-0000-0000-000000000007','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','NORMAL','PENDING',FALSE, now() + interval '30 minutes');
  BEGIN
    UPDATE migration_requests SET mode='FORCED' WHERE id='70000000-0000-0000-0000-000000000007';
    INSERT INTO tmp_case_results VALUES ('DB-C07', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS v_state = RETURNED_SQLSTATE, v_msg = MESSAGE_TEXT;
    v_key := _extract_error_key(v_msg);
    INSERT INTO tmp_case_results VALUES ('DB-C07', (v_state='P0001' AND v_key='MIGRATION_IMMUTABLE_FIELDS'), v_msg, v_state, v_key, NULL, NULL);
  END;
END $$;

-- DB-C08
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_key TEXT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO migration_requests(id,user_id,from_device_id,to_device_id,mode,status,recovery_code_verified,expires_at)
  VALUES ('70000000-0000-0000-0000-000000000008','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','NORMAL','PENDING',FALSE, now() + interval '30 minutes');
  BEGIN
    UPDATE migration_requests SET expires_at = expires_at + interval '10 minutes' WHERE id='70000000-0000-0000-0000-000000000008';
    INSERT INTO tmp_case_results VALUES ('DB-C08', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS v_state = RETURNED_SQLSTATE, v_msg = MESSAGE_TEXT;
    v_key := _extract_error_key(v_msg);
    INSERT INTO tmp_case_results VALUES ('DB-C08', (v_state='P0001' AND v_key='MIGRATION_IMMUTABLE_FIELDS'), v_msg, v_state, v_key, NULL, NULL);
  END;
END $$;

-- DB-C09
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_key TEXT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO migration_requests(id,user_id,from_device_id,to_device_id,mode,status,recovery_code_verified,expires_at)
  VALUES ('70000000-0000-0000-0000-000000000009','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','NORMAL','PENDING',FALSE, now() + interval '1 second');
  PERFORM pg_sleep(1.2);
  BEGIN
    UPDATE migration_requests SET status='CONFIRMED' WHERE id='70000000-0000-0000-0000-000000000009';
    INSERT INTO tmp_case_results VALUES ('DB-C09', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS v_state = RETURNED_SQLSTATE, v_msg = MESSAGE_TEXT;
    v_key := _extract_error_key(v_msg);
    INSERT INTO tmp_case_results VALUES ('DB-C09', (v_state='P0001' AND v_key='MIGRATION_TRANSITION_INVALID'), v_msg, v_state, v_key, NULL, NULL);
  END;
END $$;

-- DB-C10
DO $$
DECLARE v_old_status migration_status;
DECLARE v_new_cnt INT := 0;
DECLARE v_state TEXT;
DECLARE v_msg TEXT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO migration_requests(id,user_id,from_device_id,to_device_id,mode,status,recovery_code_verified,expires_at)
  VALUES ('70000000-0000-0000-0000-000000000010','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000102','NORMAL','PENDING',FALSE, now() + interval '1 second');
  PERFORM pg_sleep(1.2);
  BEGIN
    INSERT INTO migration_requests(id,user_id,from_device_id,to_device_id,mode,status,recovery_code_verified,expires_at)
    VALUES ('70000000-0000-0000-0000-000000000011','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101','00000000-0000-0000-0000-000000000103','NORMAL','PENDING',FALSE, now() + interval '30 minutes');
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS v_state = RETURNED_SQLSTATE, v_msg = MESSAGE_TEXT;
    SELECT status INTO v_old_status FROM migration_requests WHERE id='70000000-0000-0000-0000-000000000010';
    INSERT INTO tmp_case_results VALUES ('DB-C10', FALSE, coalesce(v_msg,'new insert failed'), v_state, NULL, NULL, format('old_status=%s', v_old_status));
    RETURN;
  END;

  SELECT status INTO v_old_status FROM migration_requests WHERE id='70000000-0000-0000-0000-000000000010';
  SELECT count(*) INTO v_new_cnt FROM migration_requests WHERE id='70000000-0000-0000-0000-000000000011' AND status='PENDING';

  INSERT INTO tmp_case_results VALUES ('DB-C10', (v_old_status='EXPIRED' AND v_new_cnt=1), format('old_status=%s, new_pending=%s', v_old_status, v_new_cnt), NULL,NULL,NULL,NULL);
END $$;

-- DB-C11
DO $$
DECLARE v_version BIGINT;
DECLARE v_revoked_cnt INT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO web_read_bindings(id,user_id,pairing_code_version,token_hash,status)
  VALUES
    ('80000000-0000-0000-0000-000000000011','00000000-0000-0000-0000-000000000001',1,'token-hash-11','ACTIVE'),
    ('80000000-0000-0000-0000-000000000012','00000000-0000-0000-0000-000000000001',1,'token-hash-12','ACTIVE');

  PERFORM rotate_pairing_code('00000000-0000-0000-0000-000000000001','22334455');

  SELECT pairing_code_version INTO v_version FROM users WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO v_revoked_cnt FROM web_read_bindings WHERE user_id='00000000-0000-0000-0000-000000000001' AND status='REVOKED';

  INSERT INTO tmp_case_results VALUES ('DB-C11', (v_version=2 AND v_revoked_cnt=2), format('pairing_code_version=%s, revoked_bindings=%s', v_version, v_revoked_cnt), NULL,NULL,NULL,NULL);
END $$;

-- DB-C12
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_key TEXT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO web_read_bindings(id,user_id,pairing_code_version,token_hash,status)
  VALUES ('80000000-0000-0000-0000-000000000021','00000000-0000-0000-0000-000000000001',1,'token-hash-21','ACTIVE');
  UPDATE web_read_bindings SET status='REVOKED', revoked_at=now() WHERE id='80000000-0000-0000-0000-000000000021';

  BEGIN
    UPDATE web_read_bindings SET status='ACTIVE', revoked_at=NULL WHERE id='80000000-0000-0000-0000-000000000021';
    INSERT INTO tmp_case_results VALUES ('DB-C12', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS v_state = RETURNED_SQLSTATE, v_msg = MESSAGE_TEXT;
    v_key := _extract_error_key(v_msg);
    INSERT INTO tmp_case_results VALUES ('DB-C12', (v_state='P0001' AND v_key='WEB_BINDING_REACTIVATE_DENIED'), v_msg, v_state, v_key, NULL, NULL);
  END;
END $$;

-- DB-C13
DO $$
DECLARE
  b_punch INT; a_punch INT;
  b_leave INT; a_leave INT;
  b_day INT; a_day INT;
  b_month INT; a_month INT;
  b_sync INT; a_sync INT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO sync_commits(id,user_id,device_id,writer_epoch,sync_id,payload_hash,status)
  VALUES ('50000000-0000-0000-0000-000000000013','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101',1,'60000000-0000-0000-0000-000000000013','hash-same','APPLIED');

  SELECT count(*) INTO b_punch FROM punch_records WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO b_leave FROM leave_records WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO b_day FROM day_summaries WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO b_month FROM month_summaries WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO b_sync FROM sync_commits WHERE user_id='00000000-0000-0000-0000-000000000001';

  INSERT INTO sync_commits(id,user_id,device_id,writer_epoch,sync_id,payload_hash,status)
  VALUES ('50000000-0000-0000-0000-000000000113','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101',1,'60000000-0000-0000-0000-000000000013','hash-same','APPLIED')
  ON CONFLICT (user_id, sync_id) DO NOTHING;

  SELECT count(*) INTO a_punch FROM punch_records WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO a_leave FROM leave_records WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO a_day FROM day_summaries WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO a_month FROM month_summaries WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO a_sync FROM sync_commits WHERE user_id='00000000-0000-0000-0000-000000000001';

  INSERT INTO tmp_case_results VALUES ('DB-C13',
    (a_punch-b_punch=0 AND a_leave-b_leave=0 AND a_day-b_day=0 AND a_month-b_month=0 AND a_sync-b_sync=0),
    format('delta punch=%s, leave=%s, day=%s, month=%s, sync=%s', a_punch-b_punch, a_leave-b_leave, a_day-b_day, a_month-b_month, a_sync-b_sync),
    NULL,NULL,NULL,'replay validated via ON CONFLICT DO NOTHING');

  INSERT INTO tmp_side_effect_matrix VALUES
    ('DB-C13','punch_records', a_punch-b_punch, 0, 0),
    ('DB-C13','leave_records', a_leave-b_leave, 0, 0),
    ('DB-C13','day_summaries', a_day-b_day, 0, 0),
    ('DB-C13','month_summaries', a_month-b_month, 0, 0),
    ('DB-C13','sync_commits', a_sync-b_sync, 0, 0);
END $$;

-- DB-C14
DO $$
DECLARE
  v_state TEXT; v_msg TEXT; v_con TEXT;
  b_punch INT; a_punch INT;
  b_leave INT; a_leave INT;
  b_day INT; a_day INT;
  b_month INT; a_month INT;
  b_sync INT; a_sync INT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO sync_commits(id,user_id,device_id,writer_epoch,sync_id,payload_hash,status)
  VALUES ('50000000-0000-0000-0000-000000000014','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101',1,'60000000-0000-0000-0000-000000000014','hash-old','APPLIED');

  SELECT count(*) INTO b_punch FROM punch_records WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO b_leave FROM leave_records WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO b_day FROM day_summaries WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO b_month FROM month_summaries WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO b_sync FROM sync_commits WHERE user_id='00000000-0000-0000-0000-000000000001';

  BEGIN
    INSERT INTO sync_commits(id,user_id,device_id,writer_epoch,sync_id,payload_hash,status)
    VALUES ('50000000-0000-0000-0000-000000000114','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101',1,'60000000-0000-0000-0000-000000000014','hash-new','APPLIED');
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS v_state = RETURNED_SQLSTATE, v_msg = MESSAGE_TEXT, v_con = CONSTRAINT_NAME;
  END;

  SELECT count(*) INTO a_punch FROM punch_records WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO a_leave FROM leave_records WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO a_day FROM day_summaries WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO a_month FROM month_summaries WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO a_sync FROM sync_commits WHERE user_id='00000000-0000-0000-0000-000000000001';

  INSERT INTO tmp_case_results VALUES ('DB-C14',
    (v_state='23505' AND v_con='uq_sync_commits_user_sync' AND a_punch-b_punch=0 AND a_leave-b_leave=0 AND a_day-b_day=0 AND a_month-b_month=0 AND a_sync-b_sync=0),
    coalesce(v_msg,'no error'), v_state, NULL, v_con,
    format('delta punch=%s, leave=%s, day=%s, month=%s, sync=%s', a_punch-b_punch, a_leave-b_leave, a_day-b_day, a_month-b_month, a_sync-b_sync)
  );

  INSERT INTO tmp_side_effect_matrix VALUES
    ('DB-C14','punch_records', a_punch-b_punch, 0, 0),
    ('DB-C14','leave_records', a_leave-b_leave, 0, 0),
    ('DB-C14','day_summaries', a_day-b_day, 0, 0),
    ('DB-C14','month_summaries', a_month-b_month, 0, 0),
    ('DB-C14','sync_commits', a_sync-b_sync, 0, 0);
END $$;

-- DB-C15
DO $$
DECLARE
  b_punch INT; a_punch INT;
  b_leave INT; a_leave INT;
  b_day INT; a_day INT;
  b_month INT; a_month INT;
  b_sync INT; a_sync INT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO punch_records(id, user_id, local_date, type, at_utc, timezone_id, minute_of_day, source, version)
  VALUES ('10000000-0000-0000-0000-000000000015', '00000000-0000-0000-0000-000000000001', DATE '2026-02-14', 'START', '2026-02-14 01:00:00+00', 'UTC', 60, 'MANUAL', 5);

  SELECT count(*) INTO b_punch FROM punch_records WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO b_leave FROM leave_records WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO b_day FROM day_summaries WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO b_month FROM month_summaries WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO b_sync FROM sync_commits WHERE user_id='00000000-0000-0000-0000-000000000001';

  INSERT INTO sync_commits(id,user_id,device_id,writer_epoch,sync_id,payload_hash,status)
  VALUES ('50000000-0000-0000-0000-000000000015','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101',1,'60000000-0000-0000-0000-000000000015','hash-low-version','APPLIED');

  SELECT count(*) INTO a_punch FROM punch_records WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO a_leave FROM leave_records WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO a_day FROM day_summaries WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO a_month FROM month_summaries WHERE user_id='00000000-0000-0000-0000-000000000001';
  SELECT count(*) INTO a_sync FROM sync_commits WHERE user_id='00000000-0000-0000-0000-000000000001';

  INSERT INTO tmp_case_results VALUES ('DB-C15',
    (a_punch-b_punch=0 AND a_leave-b_leave=0 AND a_day-b_day=0 AND a_month-b_month=0 AND a_sync-b_sync=1),
    format('delta punch=%s, leave=%s, day=%s, month=%s, sync=%s', a_punch-b_punch, a_leave-b_leave, a_day-b_day, a_month-b_month, a_sync-b_sync),
    NULL,NULL,NULL,'current DB behavior: only sync_commits row is inserted'
  );

  INSERT INTO tmp_side_effect_matrix VALUES
    ('DB-C15','punch_records', a_punch-b_punch, 0, 0),
    ('DB-C15','leave_records', a_leave-b_leave, 0, 0),
    ('DB-C15','day_summaries', a_day-b_day, 0, 0),
    ('DB-C15','month_summaries', a_month-b_month, 0, 0),
    ('DB-C15','sync_commits', a_sync-b_sync, 0, 0);
END $$;

-- DB-C16
DO $$
DECLARE v_old BIGINT; v_new BIGINT; v_sync_cnt INT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO punch_records(id, user_id, local_date, type, at_utc, timezone_id, minute_of_day, source, version)
  VALUES ('10000000-0000-0000-0000-000000000016', '00000000-0000-0000-0000-000000000001', DATE '2026-02-15', 'START', '2026-02-15 01:00:00+00', 'UTC', 60, 'MANUAL', 1);

  SELECT version INTO v_old FROM punch_records WHERE id='10000000-0000-0000-0000-000000000016';

  UPDATE punch_records
     SET at_utc='2026-02-15 02:00:00+00', minute_of_day=120, version=2, updated_at=now()
   WHERE id='10000000-0000-0000-0000-000000000016';

  INSERT INTO sync_commits(id,user_id,device_id,writer_epoch,sync_id,payload_hash,status)
  VALUES ('50000000-0000-0000-0000-000000000016','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000101',1,'60000000-0000-0000-0000-000000000016','hash-high-version','APPLIED');

  SELECT version INTO v_new FROM punch_records WHERE id='10000000-0000-0000-0000-000000000016';
  SELECT count(*) INTO v_sync_cnt FROM sync_commits WHERE sync_id='60000000-0000-0000-0000-000000000016';

  INSERT INTO tmp_case_results VALUES ('DB-C16', (v_old=1 AND v_new=2 AND v_sync_cnt=1), format('version %s -> %s, sync_rows=%s', v_old, v_new, v_sync_cnt), NULL,NULL,NULL,NULL);
END $$;

-- DB-C17
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_key TEXT;
BEGIN
  PERFORM _reset_base_data();
  BEGIN
    INSERT INTO sync_commits(id,user_id,device_id,writer_epoch,sync_id,payload_hash,status)
    VALUES ('50000000-0000-0000-0000-000000000017','00000000-0000-0000-0000-000000000001','00000000-0000-0000-0000-000000000102',1,'60000000-0000-0000-0000-000000000017','hash-stale','APPLIED');
    INSERT INTO tmp_case_results VALUES ('DB-C17', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS v_state = RETURNED_SQLSTATE, v_msg = MESSAGE_TEXT;
    v_key := _extract_error_key(v_msg);
    INSERT INTO tmp_case_results VALUES ('DB-C17', (v_state='P0001' AND v_key='SYNC_COMMIT_STALE_WRITER'), v_msg, v_state, v_key, NULL, NULL);
  END;
END $$;

-- DB-C18
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_key TEXT;
BEGIN
  PERFORM _reset_base_data();
  BEGIN
    INSERT INTO punch_records(id,user_id,local_date,type,at_utc,timezone_id,minute_of_day,source,version)
    VALUES ('10000000-0000-0000-0000-000000000018','00000000-0000-0000-0000-000000000001',DATE '2026-02-16','END','2026-02-16 10:00:00+00','UTC',600,'MANUAL',1);
    SET CONSTRAINTS ALL IMMEDIATE;
    INSERT INTO tmp_case_results VALUES ('DB-C18', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    SET CONSTRAINTS ALL DEFERRED;
    GET STACKED DIAGNOSTICS v_state = RETURNED_SQLSTATE, v_msg = MESSAGE_TEXT;
    v_key := _extract_error_key(v_msg);
    INSERT INTO tmp_case_results VALUES ('DB-C18', (v_state='P0001' AND v_key='PUNCH_END_REQUIRES_START'), v_msg, v_state, v_key, NULL, NULL);
  END;
END $$;

-- DB-C19
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_key TEXT;
BEGIN
  PERFORM _reset_base_data();
  BEGIN
    INSERT INTO leave_records(id,user_id,local_date,leave_type,version)
    VALUES ('20000000-0000-0000-0000-000000000019','00000000-0000-0000-0000-000000000001',DATE '2026-02-17','FULL_DAY',1);
    INSERT INTO punch_records(id,user_id,local_date,type,at_utc,timezone_id,minute_of_day,source,version)
    VALUES ('10000000-0000-0000-0000-000000000019','00000000-0000-0000-0000-000000000001',DATE '2026-02-17','START','2026-02-17 01:00:00+00','UTC',60,'AUTO',1);
    SET CONSTRAINTS ALL IMMEDIATE;
    INSERT INTO tmp_case_results VALUES ('DB-C19', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    SET CONSTRAINTS ALL DEFERRED;
    GET STACKED DIAGNOSTICS v_state = RETURNED_SQLSTATE, v_msg = MESSAGE_TEXT;
    v_key := _extract_error_key(v_msg);
    INSERT INTO tmp_case_results VALUES ('DB-C19', (v_state='P0001' AND v_key='AUTO_PUNCH_ON_FULL_DAY_LEAVE'), v_msg, v_state, v_key, NULL, NULL);
  END;
END $$;

-- DB-C20
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_key TEXT;
BEGIN
  PERFORM _reset_base_data();
  BEGIN
    INSERT INTO punch_records(id,user_id,local_date,type,at_utc,timezone_id,minute_of_day,source,version)
    VALUES ('10000000-0000-0000-0000-000000000020','00000000-0000-0000-0000-000000000001',DATE '2026-02-18','START','2026-02-18 01:00:00+00','UTC',60,'AUTO',1);
    INSERT INTO leave_records(id,user_id,local_date,leave_type,version)
    VALUES ('20000000-0000-0000-0000-000000000020','00000000-0000-0000-0000-000000000001',DATE '2026-02-18','FULL_DAY',1);
    SET CONSTRAINTS ALL IMMEDIATE;
    INSERT INTO tmp_case_results VALUES ('DB-C20', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    SET CONSTRAINTS ALL DEFERRED;
    GET STACKED DIAGNOSTICS v_state = RETURNED_SQLSTATE, v_msg = MESSAGE_TEXT;
    v_key := _extract_error_key(v_msg);
    INSERT INTO tmp_case_results VALUES ('DB-C20', (v_state='P0001' AND v_key='FULL_DAY_LEAVE_WITH_AUTO_PUNCH'), v_msg, v_state, v_key, NULL, NULL);
  END;
END $$;

-- DB-C21
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_con TEXT;
BEGIN
  PERFORM _reset_base_data();
  BEGIN
    INSERT INTO punch_records(id,user_id,local_date,type,at_utc,timezone_id,minute_of_day,source,version)
    VALUES ('10000000-0000-0000-0000-000000000021','00000000-0000-0000-0000-000000000001',DATE '2026-02-19','START','2026-02-19 01:10:30+00','UTC',70,'MANUAL',1);
    INSERT INTO tmp_case_results VALUES ('DB-C21', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS v_state=RETURNED_SQLSTATE, v_msg=MESSAGE_TEXT, v_con=CONSTRAINT_NAME;
    INSERT INTO tmp_case_results VALUES ('DB-C21', (v_state='23514' AND v_con='ck_punch_records_at_utc_minute_precision'), v_msg, v_state, NULL, v_con, NULL);
  END;
END $$;

-- DB-C22
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_con TEXT;
BEGIN
  PERFORM _reset_base_data();
  BEGIN
    INSERT INTO punch_records(id,user_id,local_date,type,at_utc,timezone_id,minute_of_day,source,version)
    VALUES ('10000000-0000-0000-0000-000000000022','00000000-0000-0000-0000-000000000001',DATE '2026-02-20','START','2026-02-20 01:10:00+00','UTC',999,'MANUAL',1);
    INSERT INTO tmp_case_results VALUES ('DB-C22', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS v_state=RETURNED_SQLSTATE, v_msg=MESSAGE_TEXT, v_con=CONSTRAINT_NAME;
    INSERT INTO tmp_case_results VALUES ('DB-C22', (v_state='23514' AND v_con IN ('ck_punch_records_local_date_match_timezone','ck_punch_records_minute_of_day_match_at_utc')), v_msg, v_state, NULL, v_con, NULL);
  END;
END $$;

-- DB-C23
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_key TEXT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO punch_records(id,user_id,local_date,type,at_utc,timezone_id,minute_of_day,source,version)
  VALUES ('10000000-0000-0000-0000-000000000023','00000000-0000-0000-0000-000000000001',DATE '2026-02-21','START','2026-02-21 01:00:00+00','UTC',60,'MANUAL',1);
  BEGIN
    UPDATE punch_records SET user_id='00000000-0000-0000-0000-000000000002' WHERE id='10000000-0000-0000-0000-000000000023';
    INSERT INTO tmp_case_results VALUES ('DB-C23', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS v_state=RETURNED_SQLSTATE, v_msg=MESSAGE_TEXT;
    v_key := _extract_error_key(v_msg);
    INSERT INTO tmp_case_results VALUES ('DB-C23', (v_state='P0001' AND v_key='RECORD_USER_ID_IMMUTABLE'), v_msg, v_state, v_key, NULL, NULL);
  END;
END $$;

-- DB-C24
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_key TEXT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO leave_records(id,user_id,local_date,leave_type,version)
  VALUES ('20000000-0000-0000-0000-000000000024','00000000-0000-0000-0000-000000000001',DATE '2026-02-22','AM',1);
  BEGIN
    UPDATE leave_records SET user_id='00000000-0000-0000-0000-000000000002' WHERE id='20000000-0000-0000-0000-000000000024';
    INSERT INTO tmp_case_results VALUES ('DB-C24', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS v_state=RETURNED_SQLSTATE, v_msg=MESSAGE_TEXT;
    v_key := _extract_error_key(v_msg);
    INSERT INTO tmp_case_results VALUES ('DB-C24', (v_state='P0001' AND v_key='RECORD_USER_ID_IMMUTABLE'), v_msg, v_state, v_key, NULL, NULL);
  END;
END $$;

-- DB-C25
DO $$
DECLARE v_blocked_cnt INT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO security_attempt_windows(id,scene,subject_hash,client_fingerprint_hash,window_start,fail_count,blocked_until)
  VALUES
    ('90000000-0000-0000-0000-000000000251','WEB_PAIR_BIND','sub_u1','fp_1','2000-01-01 00:00:00+00',6, now() + interval '30 minutes'),
    ('90000000-0000-0000-0000-000000000252','WEB_PAIR_BIND','sub_u1','GLOBAL','2000-01-01 00:00:00+00',16, now() + interval '2 hours'),
    ('90000000-0000-0000-0000-000000000253','WEB_PAIR_BIND','GLOBAL','fp_1','2000-01-01 00:00:00+00',16, now() + interval '2 hours');

  SELECT count(*) INTO v_blocked_cnt
  FROM security_attempt_windows
  WHERE scene='WEB_PAIR_BIND'
    AND (
      (subject_hash='sub_u1' AND client_fingerprint_hash='fp_1')
      OR (subject_hash='sub_u1' AND client_fingerprint_hash='GLOBAL')
      OR (subject_hash='GLOBAL' AND client_fingerprint_hash='fp_1')
    )
    AND blocked_until > now();

  INSERT INTO tmp_case_results VALUES ('DB-C25', (v_blocked_cnt >= 1), format('blocked_rows=%s', v_blocked_cnt), NULL,NULL,NULL,'blocked hit found across 3 dimensions');
END $$;

-- DB-C26
DO $$
DECLARE v_blocked_cnt INT;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO security_attempt_windows(id,scene,subject_hash,client_fingerprint_hash,window_start,fail_count,blocked_until)
  VALUES
    ('90000000-0000-0000-0000-000000000261','WEB_PAIR_BIND','sub_u1','fp_1','2000-01-01 00:00:00+00',6, now() - interval '1 minute');

  SELECT count(*) INTO v_blocked_cnt
  FROM security_attempt_windows
  WHERE scene='WEB_PAIR_BIND'
    AND subject_hash='sub_u1'
    AND client_fingerprint_hash='fp_1'
    AND blocked_until > now();

  INSERT INTO tmp_case_results VALUES ('DB-C26', (v_blocked_cnt = 0), format('active_blocked_rows=%s', v_blocked_cnt), NULL,NULL,NULL,'unblocked after window expiration');
END $$;

-- DB-C27
DO $$
DECLARE v_input TIMESTAMPTZ := '2000-01-01 00:00:00+00';
DECLARE v_actual TIMESTAMPTZ;
DECLARE v_sec NUMERIC;
DECLARE v_min NUMERIC;
BEGIN
  PERFORM _reset_base_data();
  INSERT INTO security_attempt_windows(id,scene,subject_hash,client_fingerprint_hash,window_start,fail_count,blocked_until)
  VALUES ('90000000-0000-0000-0000-000000000271','WEB_PAIR_BIND','sub_u1','fp_1',v_input,1,NULL);

  SELECT window_start INTO v_actual FROM security_attempt_windows WHERE id='90000000-0000-0000-0000-000000000271';
  SELECT EXTRACT(SECOND FROM v_actual), EXTRACT(MINUTE FROM v_actual) INTO v_sec, v_min;

  INSERT INTO tmp_case_results VALUES (
    'DB-C27',
    (v_actual <> v_input AND mod(v_min::INT, 10)=0 AND v_sec = 0),
    format('input=%s, normalized=%s', v_input, v_actual),
    NULL,NULL,NULL,
    'window_start normalized by UTC override'
  );
END $$;

-- DB-C28
DO $$
DECLARE v_state TEXT; v_msg TEXT; v_con TEXT;
BEGIN
  PERFORM _reset_base_data();
  BEGIN
    UPDATE users SET pairing_code='12345ABC' WHERE user_id='00000000-0000-0000-0000-000000000001';
    INSERT INTO tmp_case_results VALUES ('DB-C28', FALSE, 'unexpected success', NULL,NULL,NULL,NULL);
  EXCEPTION WHEN OTHERS THEN
    GET STACKED DIAGNOSTICS v_state=RETURNED_SQLSTATE, v_msg=MESSAGE_TEXT, v_con=CONSTRAINT_NAME;
    INSERT INTO tmp_case_results VALUES ('DB-C28', (v_state='23514' AND v_con='ck_users_pairing_code_format'), v_msg, v_state, NULL, v_con, NULL);
  END;
END $$;

SELECT case_id, pass, actual_result, coalesce(sqlstate,'-') AS sqlstate, coalesce(error_key,'-') AS error_key, coalesce(constraint_name,'-') AS constraint_name, coalesce(note,'-') AS note
FROM tmp_case_results
ORDER BY case_id;

SELECT case_id, table_name, insert_delta, update_delta, delete_delta
FROM tmp_side_effect_matrix
ORDER BY case_id, table_name;
