-- DEV: enrich the two sample stores + add four more so the Explore screen
-- matches the Gurufuri design mockups. Removable like 000003.

UPDATE stores SET
    name = '米粉キッチン こめこ', cuisine = '定食・カフェ', price_level = 3, rating = 4.8,
    review_count = 312, nearest_station = '渋谷駅 ハチ公口・徒歩4分',
    blurb = '全メニュー米粉。専用フライヤーで揚げ物も安心。',
    gf_status = 'certified', is_gf_oriented = true
WHERE id = '11111111-1111-1111-1111-111111111111';

UPDATE stores SET
    name = '玄 -GEN- グルテンフリー製麺', cuisine = 'ラーメン', price_level = 2, rating = 4.7,
    review_count = 528, nearest_station = '新宿三丁目駅 C2',
    blurb = '米粉100%の自家製麺ラーメン。小麦不使用の厨房。',
    gf_status = 'certified', is_gf_oriented = true
WHERE id = '22222222-2222-2222-2222-222222222222';

INSERT INTO stores
    (id, ward_id, name, address, latitude, longitude, is_gf_oriented, opening_hours, status, approved_at,
     cuisine, price_level, rating, review_count, nearest_station, blurb, gf_status)
VALUES
    ('33333333-3333-3333-3333-333333333333', (SELECT id FROM wards WHERE name_en = 'Meguro'),
     'こめ粉ベーカリー Sora', '東京都目黒区自由が丘2-1-1', 35.6076, 139.6689, true,
     '[{"day":1,"open":"0800","close":"1900"},{"day":2,"open":"0800","close":"1900"},{"day":3,"open":"0800","close":"1900"}]'::jsonb,
     'approved', now(), 'ベーカリー', 2, 4.9, 204, '自由が丘駅 正面口・徒歩3分',
     '小麦不使用のベーカリー。米粉パンと焼き菓子。', 'certified'),
    ('44444444-4444-4444-4444-444444444444', (SELECT id FROM wards WHERE name_en = 'Taito'),
     '抹茶 & Co. 和カフェ', '東京都台東区浅草1-2-3', 35.7148, 139.7967, false,
     '[{"day":3,"open":"1100","close":"1800"},{"day":4,"open":"1100","close":"1800"}]'::jsonb,
     'approved', now(), '和菓子・カフェ', 2, 4.6, 156, '浅草駅 A4・徒歩5分',
     '抹茶スイーツ。GF対応は要相談。', 'on_request'),
    ('55555555-5555-5555-5555-555555555555', (SELECT id FROM wards WHERE name_en = 'Setagaya'),
     'ひなたカレー', '東京都世田谷区三軒茶屋1-1-1', 35.6435, 139.6695, false,
     '[{"day":5,"open":"1130","close":"2100"},{"day":6,"open":"1130","close":"2100"}]'::jsonb,
     'approved', now(), 'カレー', 1, 4.5, 98, '三軒茶屋駅 南口・徒歩2分',
     'スパイスカレー。ルー不使用、GFは要相談。', 'on_request'),
    ('66666666-6666-6666-6666-666666666666', (SELECT id FROM wards WHERE name_en = 'Taito'),
     '浅草お好み 和', '東京都台東区浅草2-3-4', 35.7142, 139.7966, false,
     '[{"day":6,"open":"1200","close":"2200"},{"day":0,"open":"1200","close":"2200"}]'::jsonb,
     'approved', now(), 'お好み焼き', 2, 4.2, 73, '浅草駅 1番・徒歩6分',
     '一部メニューに小麦使用。注意点あり。', 'contains_hidden_gluten');
