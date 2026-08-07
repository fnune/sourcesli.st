-- Undoing what the bug tracker's text went through before it reached us.
--
-- A name that was UTF-8, read once as Latin-1 and written back out as UTF-8,
-- arrives as its own bytes spelled out: "Frédéric" as "FrÃ©dÃ©ric". It is most
-- of the bug submitters whose names are not plain ASCII, and not one row of any
-- package table, so the damage is upstream of the dump and older than anything
-- we can reach. We cannot mend it there, and we can undo it exactly here,
-- because the two encodings that made it are the two that reverse it.
--
-- The undoing is also the test of whether there was damage: reading the
-- characters back as Latin-1 bytes only yields valid UTF-8 when that is what
-- they were. A name that is merely accented fails the round trip and is
-- returned untouched, and a title in a script Latin-1 has no room for fails
-- earlier still, at convert_to. So the function may be pointed at any column
-- without a list of which rows are broken.

CREATE FUNCTION repair_mojibake(value text) RETURNS text
LANGUAGE plpgsql IMMUTABLE STRICT PARALLEL SAFE
AS $$
DECLARE
    candidate text := value;
    undone text;
BEGIN
    -- A handful of names went through the encoding twice, so the undoing
    -- repeats until it stops changing anything. The bound is there only so a
    -- string that somehow reverses into itself cannot spin.
    FOR _ IN 1..4 LOOP
        -- What the damage looks like: a lead byte turned into a letter, and
        -- behind it the continuation bytes that belonged to it. Nothing else
        -- can be double-encoded text, and testing it first keeps the
        -- subtransaction that follows off almost every row.
        IF candidate !~ '[\u00c2-\u00f4][\u0080-\u00bf]' THEN
            EXIT;
        END IF;

        BEGIN
            undone := convert_from(convert_to(candidate, 'LATIN1'), 'UTF8');
        EXCEPTION WHEN others THEN
            -- Whoever broke it may have read the bytes as Windows-1252, which
            -- differs from Latin-1 exactly where the quotes and dashes live.
            BEGIN
                undone := convert_from(convert_to(candidate, 'WIN1252'), 'UTF8');
            EXCEPTION WHEN others THEN
                EXIT;
            END;
        END;

        EXIT WHEN undone = candidate;
        candidate := undone;
    END LOOP;

    -- The C1 controls are not text anybody typed. Reaching them means the round
    -- trip found a reading of the bytes rather than the reading, and the string
    -- we were given is the better guess.
    IF candidate ~ '[\u0080-\u009f]' THEN
        RETURN value;
    END IF;

    RETURN candidate;
END;
$$;
