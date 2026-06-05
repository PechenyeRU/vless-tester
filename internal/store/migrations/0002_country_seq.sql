-- Per-country counter backing stable sequence names (FR110, CH23, ...).
CREATE TABLE country_seq (
    country    TEXT PRIMARY KEY,
    next_index INT NOT NULL
);
