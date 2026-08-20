-- POC-shaped schema: enough tablets that leader/peer placement is visible
-- on a 3-AZ RF=3 universe, plus a skewed table for hot-tablet signal.

DROP TABLE IF EXISTS demo_orders;
DROP TABLE IF EXISTS demo_events;
DROP TABLE IF EXISTS demo_sessions;

CREATE TABLE demo_orders (
  id          uuid        NOT NULL,
  customer_id int         NOT NULL,
  amount      numeric     NOT NULL,
  payload     text,
  created_at  timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (id HASH)
) SPLIT INTO 24 TABLETS;

CREATE TABLE demo_events (
  user_id int  NOT NULL,
  seq     int  NOT NULL,
  kind    text NOT NULL,
  payload text,
  PRIMARY KEY ((user_id) HASH, seq)
) SPLIT INTO 36 TABLETS;

-- Intentionally skewed: almost every write hashes to one tablet.
CREATE TABLE demo_sessions (
  bucket  int  NOT NULL,
  seq     int  NOT NULL,
  payload text,
  PRIMARY KEY ((bucket) HASH, seq)
) SPLIT INTO 12 TABLETS;

INSERT INTO demo_orders (id, customer_id, amount, payload)
SELECT gen_random_uuid(),
       (random() * 5000)::int,
       round((random() * 250)::numeric, 2),
       repeat('order', 40)
FROM generate_series(1, 80000);

INSERT INTO demo_events (user_id, seq, kind, payload)
SELECT (random() * 20000)::int,
       g,
       (ARRAY['click', 'view', 'purchase', 'refund'])[1 + (random() * 3)::int],
       repeat('evt', 30)
FROM generate_series(1, 80000) AS g;

INSERT INTO demo_sessions (bucket, seq, payload)
SELECT 1, g, repeat('sess', 80)
FROM generate_series(1, 40000) AS g;

UPDATE demo_orders
SET payload = repeat('upd', 60)
WHERE customer_id % 17 = 0;

ANALYZE demo_orders;
ANALYZE demo_events;
ANALYZE demo_sessions;
