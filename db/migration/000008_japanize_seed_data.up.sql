-- DEV: Japanize the English fields left in the sample seed (000003) — the two
-- original stores' addresses and their menu item names/notes. The JA display
-- fields were added in 000007; this finishes the job so Store Detail and Menu
-- read fully Japanese. Removable like the other seed migrations.

UPDATE stores SET address = '東京都渋谷区道玄坂1-2-3'
WHERE id = '11111111-1111-1111-1111-111111111111';
UPDATE stores SET address = '東京都新宿区歌舞伎町4-5-6'
WHERE id = '22222222-2222-2222-2222-222222222222';

UPDATE menu_items SET name = '米粉マルゲリータピザ',
    gf_note = '認証済みの米粉を使用し、専用オーブンで焼き上げています。'
WHERE store_id = '11111111-1111-1111-1111-111111111111' AND name = 'GF Margherita Pizza';

UPDATE menu_items SET name = 'キヌアサラダボウル'
WHERE store_id = '11111111-1111-1111-1111-111111111111' AND name = 'Quinoa Salad Bowl';

UPDATE menu_items SET name = '米粉醤油ラーメン',
    gf_note = '麺は米粉麺に変更可能です（スタッフにご相談ください）。スープは標準でグルテンフリーです。'
WHERE store_id = '22222222-2222-2222-2222-222222222222' AND name = 'Rice Flour Shoyu Ramen';

UPDATE menu_items SET name = '餃子（小麦使用）',
    gf_note = '皮に小麦を使用しています。情報開示のため掲載しています。'
WHERE store_id = '22222222-2222-2222-2222-222222222222' AND name = 'Gyoza (contains wheat)';
