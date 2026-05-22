CREATE OR REPLACE FUNCTION public.kitworkid_decode(id_str text)
 RETURNS timestamp with time zone
 LANGUAGE plpgsql
 IMMUTABLE
AS $function$
DECLARE
    charset text := '0123456789abcdefghijklmnopqrstuvwxyz';
    avail_chars text := charset;
    time_len int := 13;
    charset_len int := 36;
    
    time_chars text;
    char_val char;
    idx int;
    idxs integer[] := ARRAY[]::integer[];
    
    current_t bigint := 0;
    start_base bigint := 24;
    base bigint;
    nano_delta bigint;
    epoch_nano bigint := 1765874100000000000;
BEGIN
    IF length(id_str) != 36 THEN
        RAISE EXCEPTION 'ID không hợp lệ. Chiều dài phải bằng 36 ký tự.';
    END IF;
    
    time_chars := substring(id_str from 1 for time_len);
    
    -- Dịch ngược mảng rút gọn
    FOR i IN 1..time_len LOOP
        char_val := substring(time_chars from i for 1);
        idx := position(char_val in avail_chars) - 1;
        
        IF idx < 0 THEN
            RAISE EXCEPTION 'Ký tự không hợp lệ trong ID.';
        END IF;
        
        idxs[i] := idx;
        
        avail_chars := substring(avail_chars from 1 for idx) || 
                       substring(avail_chars from (idx + 2));
    END LOOP;

    -- Tính lại Toán học Horner
    FOR i IN 1..time_len LOOP
        base := start_base + (time_len - i); 
        current_t := current_t * base + idxs[i];
    END LOOP;

    -- Phủi bỏ phần Sequence (12 bit cuối) để lấy đúng Delta gốc
    nano_delta := current_t & ~4095::bigint;
    
    -- Chuyển Nano về Giây và ép thành Timestamp (dùng Numeric để giữ tối đa độ chính xác Microsecond của Postgres)
    RETURN to_timestamp((epoch_nano + nano_delta)::numeric / 1000000000.0);
END;
$function$;
