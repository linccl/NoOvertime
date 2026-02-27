WITH ext_owned AS (
  SELECT objid
  FROM pg_depend
  WHERE deptype = 'e'
),
non_system_objects AS (
  SELECT 'table' AS obj_type, c.oid
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname !~ '^pg_'
    AND n.nspname <> 'information_schema'
    AND c.relkind IN ('r','p','v','m','S','i')
  UNION ALL
  SELECT 'type' AS obj_type, t.oid
  FROM pg_type t
  JOIN pg_namespace n ON n.oid = t.typnamespace
  WHERE n.nspname !~ '^pg_'
    AND n.nspname <> 'information_schema'
    AND t.typtype = 'e'
  UNION ALL
  SELECT 'function' AS obj_type, p.oid
  FROM pg_proc p
  JOIN pg_namespace n ON n.oid = p.pronamespace
  WHERE n.nspname !~ '^pg_'
    AND n.nspname <> 'information_schema'
)
SELECT obj_type, count(*) AS cnt
FROM non_system_objects o
LEFT JOIN ext_owned eo ON eo.objid = o.oid
WHERE eo.objid IS NULL
GROUP BY obj_type
ORDER BY obj_type;
