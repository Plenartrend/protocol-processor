-- Avg unassignes actitivies ratio --
WITH protocol_ratios AS (
    SELECT
        p.id,
        COUNT(*) FILTER (WHERE a.text = '')::float / COUNT(*)::float AS empty_ratio
    FROM protocols p
             JOIN activities a ON a.protocol_id = p.id
    WHERE p.processing_status = 'completed'
      AND a.type = 'Rede'
    GROUP BY p.id
)
SELECT AVG(empty_ratio) AS avg_empty_ratio
FROM protocol_ratios;

-- Protocols with worse than 25% unassignes activities --
SELECT
    count(a.id) as act_count,
    COUNT(*) FILTER (WHERE a.text = '')::float / COUNT(*)::float AS unassigned_rate,
    p.*
FROM protocols p
         JOIN activities a ON a.protocol_id = p.id
WHERE p.processing_status = 'completed'
  AND a.type = 'Rede'
GROUP BY p.id
HAVING COUNT(*) FILTER (WHERE a.text = '')::float / COUNT(*)::float > 0.25
ORDER BY unassigned_rate DESC;

-- Unassignment-Rate for a specific protocol --
SELECT
    p.id AS protocol_id,
    COUNT(a.id) AS act_count,
    COUNT(*) FILTER (WHERE a.text = '')::float
        / COUNT(*)::float AS unassigned_rate
FROM protocols p
         JOIN activities a ON a.protocol_id = p.id
WHERE p.id = 5750
  AND a.type = 'Rede'
GROUP BY p.id;
