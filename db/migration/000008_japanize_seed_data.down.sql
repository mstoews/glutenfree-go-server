-- Revert the Japanized sample fields back to the original English (000003).

UPDATE stores SET address = '1-2-3 Dogenzaka, Shibuya-ku, Tokyo'
WHERE id = '11111111-1111-1111-1111-111111111111';
UPDATE stores SET address = '4-5-6 Kabukicho, Shinjuku-ku, Tokyo'
WHERE id = '22222222-2222-2222-2222-222222222222';

UPDATE menu_items SET name = 'GF Margherita Pizza',
    gf_note = 'Made with certified GF flour baked in a dedicated oven.'
WHERE store_id = '11111111-1111-1111-1111-111111111111' AND name = '米粉マルゲリータピザ';

UPDATE menu_items SET name = 'Quinoa Salad Bowl'
WHERE store_id = '11111111-1111-1111-1111-111111111111' AND name = 'キヌアサラダボウル';

UPDATE menu_items SET name = 'Rice Flour Shoyu Ramen',
    gf_note = 'Ask staff for the GF noodle swap; broth is GF by default.'
WHERE store_id = '22222222-2222-2222-2222-222222222222' AND name = '米粉醤油ラーメン';

UPDATE menu_items SET name = 'Gyoza (contains wheat)',
    gf_note = 'Wrappers contain wheat — listed for transparency.'
WHERE store_id = '22222222-2222-2222-2222-222222222222' AND name = '餃子（小麦使用）';
