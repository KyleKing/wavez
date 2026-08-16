CREATE TABLE coverage (
    file TEXT NOT NULL,
    start_line INTEGER NOT NULL,
    end_line INTEGER NOT NULL,
    test TEXT NOT NULL
);

CREATE TABLE imports (
    src_pkg TEXT NOT NULL,
    dst_pkg TEXT NOT NULL
);

CREATE TABLE file_pkg (
    file TEXT NOT NULL,
    pkg TEXT NOT NULL
);

CREATE INDEX idx_coverage_file_line ON coverage(file, start_line, end_line);
CREATE INDEX idx_imports_dst ON imports(dst_pkg);
CREATE INDEX idx_imports_src ON imports(src_pkg);
CREATE INDEX idx_file_pkg_file ON file_pkg(file);
CREATE INDEX idx_file_pkg_pkg ON file_pkg(pkg);
