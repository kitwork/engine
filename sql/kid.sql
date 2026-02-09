CREATE OR REPLACE FUNCTION kid() RETURNS text AS $$
DECLARE
    -- 01. Prepare Full Charset
    original_chars text := '0123456789abcdefghijklmnopqrstuvwxyz';
    avail_chars text := original_chars; -- Mutable copy
    
    -- Time calculation (Nano precision with jitter)
    -- Postgres epoch is seconds. * 1e9 to get Nano.
    ts_val numeric := FLOOR(EXTRACT(EPOCH FROM clock_timestamp()) * 1000000000);
    jitter integer := FLOOR(random() * 1000)::integer;
    
    -- Apply jitter to last 3 digits (000-999) to add higher entropy
    t numeric := (FLOOR(ts_val / 1000) * 1000) + jitter;
    
    -- Arrays/Storage
    idxs integer[] := ARRAY[]::integer[]; -- To store 13 indexes
    current_t numeric := t;
    start_base numeric := 24; -- Base for the last timestamp character (index 12)
    base numeric;
    
    -- Result parts
    time_part text := '';
    random_part text := '';
    
    i integer;
    selected_char char;
    
    -- Helper for shuffle
    avail_arr text[]; 
    temp_char text;
    rand_idx integer;
BEGIN
    -- 02. Time Part (Mixed Radix Encoding)
    -- We need 13 digits.
    -- Loop from 12 down to 0 (LSB to MSB) corresponds to Bases 24..36
    -- Warning: PL/pgSQL arrays are 1-based usually, but we manage indexes logically.
    
    -- Calculate Indexes (Right to Left / LSB to MSB)
    FOR i IN REVERSE 12..0 LOOP
        base := start_base + (12 - i); -- 24, 25 ... 36
        
        -- Mod and Div
        -- current_t % base
        idxs[i+1] := (current_t % base)::integer; -- Store in 1-based array at i+1
        current_t := FLOOR(current_t / base);
    END LOOP;

    -- 03. Construct Time Part & Shrink Charset
    -- Apply digits to 'avail_chars'
    FOR i IN 0..12 LOOP
        -- Get the index for this position (0-based value from logic)
        -- We need to find the char at this index in current 'avail_chars'
        -- Postgres user 1-based indexing for substring
        
        -- Get index value calculated above
        rand_idx := idxs[i+1]; 
        
        -- Select char: substring(str from idx+1 for 1)
        selected_char := substring(avail_chars from (rand_idx + 1) for 1);
        time_part := time_part || selected_char;
        
        -- Remove char from avail_chars:
        -- overlay() or concatenation of parts before and after
        -- If random_idx=0 (first char), take from pos 2..end
        -- If random_idx=len-1 (last char), take from 1..len-1
        
        -- Implementation:
        -- Left part: substring(avail_chars from 1 for rand_idx) -- length is rand_idx
        -- Right part: substring(avail_chars from rand_idx + 2)
        
        avail_chars := substring(avail_chars from 1 for rand_idx) || 
                       substring(avail_chars from (rand_idx + 2));
    END LOOP;

    -- 04. Random Padding (Unique Shuffle)
    -- avail_chars now contains remaining 23 characters.
    -- Shuffle them.
    -- Simplest way in SQL: Convert string to table of chars, random sort, aggregate back.
    
    SELECT string_agg(c, '') INTO random_part
    FROM (
        SELECT substring(avail_chars from n for 1) as c
        FROM generate_series(1, length(avail_chars)) as n
        ORDER BY random()
    ) as shuffled;

    RETURN time_part || coalesce(random_part, '');
END;
$$ LANGUAGE plpgsql;
