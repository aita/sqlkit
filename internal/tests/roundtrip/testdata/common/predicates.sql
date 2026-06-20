-- Portable predicate / expression statements.

SELECT id FROM users WHERE age BETWEEN 18 AND 65;
SELECT id FROM users WHERE age NOT BETWEEN 18 AND 65;
SELECT id FROM users WHERE id IN (1, 2, 3, 4, 5);
SELECT id FROM users WHERE name NOT IN ('a', 'b');
SELECT id FROM users WHERE email IS NULL;
SELECT id FROM users WHERE email IS NOT NULL;
SELECT id FROM users WHERE name LIKE '%smith%';
SELECT id FROM users WHERE name NOT LIKE 'a%';
SELECT id FROM users WHERE age > 18 AND age < 65 AND active = true;
SELECT id FROM users WHERE age < 18 OR age > 65 OR active = false;
SELECT id FROM users WHERE (age > 18 AND active = true) OR name = 'admin';
SELECT id FROM users WHERE NOT (age > 18 AND active = true);
SELECT id FROM users WHERE age = 18 OR age = 21 OR age = 65;
SELECT id, age + 1 FROM users;
SELECT id, age * 2 FROM users;
SELECT id FROM users WHERE age + 5 > 30;
SELECT id FROM users WHERE age >= 18 AND age <= 65;
SELECT id FROM users WHERE id <> 0;
SELECT id FROM users WHERE id != 0;
SELECT id, (age - 1) * 2 FROM users;
