-- Đơn hàng theo ngày của quán; chạy trên KitDB (PostgreSQL wire).
-- Schema, a function, the reports the dashboard runs, and the writes.

CREATE TABLE IF NOT EXISTS sites (
  id          SERIAL PRIMARY KEY,
  hostname    VARCHAR(253) NOT NULL UNIQUE,
  settings    JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS orders (
  id          BIGSERIAL PRIMARY KEY,
  site_id     INTEGER NOT NULL REFERENCES sites(id) ON DELETE CASCADE,
  email       VARCHAR(320) NOT NULL CHECK (email LIKE '%@%'),
  total       NUMERIC(12, 2) NOT NULL DEFAULT 0.00,
  discount    DOUBLE PRECISION,
  note        TEXT DEFAULT 'không có ghi chú',
  paid        BOOLEAN NOT NULL DEFAULT FALSE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  "Weird Name" INTEGER,
  CONSTRAINT orders_total_positive CHECK (total >= 0)
);

CREATE UNIQUE INDEX IF NOT EXISTS orders_site_email ON orders (site_id, lower(email));
CREATE INDEX orders_created ON orders USING brin (created_at);

/* Doanh thu 7 ngày gần nhất, kể cả ngày không có đơn.
   /* A nested comment, as PostgreSQL allows. */
   Still inside the outer comment. */
WITH days AS (
  SELECT generate_series(current_date - INTERVAL '6 days', current_date, '1 day')::date AS day
)
SELECT d.day,
       COALESCE(SUM(o.total), 0) AS revenue,
       COUNT(o.id) FILTER (WHERE o.total > 100000) AS large_orders,
       round(avg(o.total)::numeric, 2) AS average
FROM days d
LEFT JOIN orders o ON o.created_at::date = d.day AND o.site_id = $1
GROUP BY d.day
HAVING COUNT(*) >= 0
ORDER BY d.day DESC NULLS LAST
LIMIT 7 OFFSET 0;

-- The best customers per site, ranked with a window.
SELECT *
FROM (
  SELECT s.hostname,
         o.email,
         sum(o.total) AS spent,
         rank() OVER (PARTITION BY s.id ORDER BY sum(o.total) DESC) AS place
  FROM orders AS o
  INNER JOIN sites AS s ON s.id = o.site_id
  WHERE o.paid IS TRUE
    AND o.created_at BETWEEN :from AND :until
    AND o.email NOT ILIKE '%@example.test'
  GROUP BY s.id, s.hostname, o.email
) ranked
WHERE place <= 3;

-- Settings live in JSON: read a key, test containment.
SELECT hostname,
       settings->>'theme' AS theme,
       settings #>> '{hours,open}' AS opens,
       settings @> '{"delivery": true}' AS delivers
FROM sites
WHERE settings ? 'theme' OR settings ?| array['menu', 'hours'];

CREATE OR REPLACE FUNCTION order_total(order_id BIGINT)
RETURNS NUMERIC
LANGUAGE plpgsql
STABLE
AS $body$
DECLARE
  result NUMERIC := 0;
BEGIN
  SELECT total INTO result FROM orders WHERE id = order_id;
  RETURN COALESCE(result, 0); -- not a comment for SQL: this is all one string
END;
$body$;

DO $$ BEGIN RAISE NOTICE 'đã tạo hàm'; END $$;

INSERT INTO orders (site_id, email, total, note)
VALUES (1, 'an@example.test', 58000, 'it''s two drinks'),
       (1, 'binh@example.test', 1.5e5, NULL),
       (2, 'chi@example.test', .5, E'hai dòng\nmột ghi chú')
ON CONFLICT (site_id, lower(email)) DO UPDATE
  SET total = orders.total + EXCLUDED.total,
      note  = EXCLUDED.note
RETURNING id, total;

INSERT INTO sites(hostname) VALUES ('quancaphe.localhost') ON CONFLICT DO NOTHING;

UPDATE orders SET note = N'đã giao', paid = TRUE WHERE id = ? AND total <> 0 OR site_id IS NOT NULL;
UPDATE orders SET discount = 0.1 WHERE id = ?1 AND email = @customer;
DELETE FROM orders WHERE created_at < now() - INTERVAL '1 year' AND NOT paid;

SELECT left(email, 3) || '…' AS masked,
       replace(note, 'ghi chú', 'note') AS translated,
       CAST(total AS INTEGER) AS whole,
       CASE WHEN total > 100000 THEN 'lớn' WHEN total > 0 THEN 'nhỏ' ELSE 'không' END AS size,
       x'CAFE' AS blob_literal,
       B'1010' AS bits
FROM orders
WHERE email SIMILAR TO '[a-z]+@%' AND id IN (1, 2, 3) AND total % 2 = 0;

-- SQLite: the same table, its own words, `backtick` names.
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS `menu items` (
  `id`    INTEGER PRIMARY KEY AUTOINCREMENT,
  name    TEXT NOT NULL COLLATE NOCASE,
  price   INTEGER NOT NULL DEFAULT 0
) STRICT;
SELECT date(created_at), datetime('now', '-1 day'), json_extract(settings, '$.theme') FROM sites;

BEGIN TRANSACTION;
SAVEPOINT before_cleanup;
select count(*) from orders where note is null; -- lower case keywords too
ROLLBACK TO SAVEPOINT before_cleanup;
COMMIT;
