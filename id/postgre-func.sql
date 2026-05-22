-- Tạo bộ đếm Atomic 12-bit (Tự động reset khi chạm 4095)
CREATE SEQUENCE IF NOT EXISTS public.kitworkid_seq 
    MINVALUE 0 
    MAXVALUE 4095 
    START WITH 0 
    CYCLE;

-- Cập nhật hàm sinh ID
CREATE OR REPLACE FUNCTION public.kitid(p_unix_nano bigint DEFAULT NULL)
 RETURNS text
 LANGUAGE plpgsql
AS $function$
DECLARE
    original_chars text := '0123456789abcdefghijklmnopqrstuvwxyz';
    avail_chars text := original_chars; 
    
    epoch_nano bigint := 1765874100000000000;
    now_nano bigint;
    seq_val bigint;
    t bigint;
    
    idxs integer[] := ARRAY[]::integer[]; 
    current_t bigint;
    start_base bigint := 24; 
    base bigint;
    
    time_part text := '';
    random_part text := '';
    i integer;
    selected_char char;
    rand_idx integer;
BEGIN
    IF p_unix_nano IS NULL THEN
        now_nano := (EXTRACT(EPOCH FROM clock_timestamp()) * 1000000000)::bigint;
    ELSE
        now_nano := p_unix_nano;
    END IF;

    IF now_nano < epoch_nano THEN
        now_nano := epoch_nano;
    END IF;

    -- Rút Sequence thay cho rand() (Đảm bảo Atomic 100%)
    seq_val := nextval('public.kitworkid_seq');

    -- Bitmask t = (t &^ 0xfff) | seq y hệt Golang (~ là toán tử NOT của Postgres)
    t := ((now_nano - epoch_nano) & ~4095::bigint) | seq_val;
    current_t := t;

    -- Cố định 13 vòng lặp Time
    FOR i IN REVERSE 12..0 LOOP
        base := start_base + (12 - i); 
        idxs[i+1] := (current_t % base)::integer; 
        current_t := current_t / base;
    END LOOP;

    -- Gắp & rút gọn
    FOR i IN 0..12 LOOP
        rand_idx := idxs[i+1]; 
        selected_char := substring(avail_chars from (rand_idx + 1) for 1);
        time_part := time_part || selected_char;
        avail_chars := substring(avail_chars from 1 for rand_idx) || 
                       substring(avail_chars from (rand_idx + 2));
    END LOOP;

    -- Shuffle phần dư
    SELECT string_agg(c, '') INTO random_part
    FROM (
        SELECT substring(avail_chars from n for 1) as c
        FROM generate_series(1, length(avail_chars)) as n
        ORDER BY random()
    ) as shuffled;

    RETURN time_part || coalesce(random_part, '');
END;
$function$;
