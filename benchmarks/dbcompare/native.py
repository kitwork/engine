"""Optional native comparators. No KitDB/Kitwork dependency or network access."""
import argparse
import json
import math
import pathlib
import sqlite3
import statistics
import sys
import time


def check(actual, expected):
    if len(actual) != len(expected):
        raise AssertionError(f"row count {len(actual)} != {len(expected)}")
    for row, want in zip(actual, expected):
        if len(row) != len(want):
            raise AssertionError("row width mismatch")
        for a, b in zip(row, want):
            if isinstance(a, (int, float)) and isinstance(b, (int, float)):
                if math.isfinite(a) and math.isclose(a, b, rel_tol=1e-9, abs_tol=1e-9):
                    continue
            elif a == b:
                continue
            raise AssertionError(f"{a!r} != {b!r}")


def measure(connection, case, repeats, warmups, engine):
    samples = []
    for i in range(warmups + repeats):
        start = time.perf_counter_ns()
        rows = connection.execute(case["sql"]).fetchall()
        elapsed = (time.perf_counter_ns() - start) / 1e6
        check(rows, case["want"])
        if i >= warmups:
            samples.append(elapsed)
    result = dict(name=case["name"], sql=case["sql"], path=engine,
                  samples_ms=samples, median_ms=statistics.median(samples),
                  min_ms=min(samples), max_ms=max(samples), rows=rows)
    print(f"  {engine:16s} {case['name']:18s} {result['median_ms']:9.3f} ms", file=sys.stderr)
    return result


def run(args):
    directory = pathlib.Path(args.directory)
    with open(args.workload, encoding="utf-8") as source:
        w = json.load(source)
    if any(directory.iterdir()):
        raise ValueError("native benchmark directory must be empty")
    if args.engine == "duckdb":
        import duckdb
        connection = duckdb.connect(str(directory / "data.duckdb"), config={
            "threads": "1", "memory_limit": "512MB",
            "autoinstall_known_extensions": "false", "autoload_known_extensions": "false",
        })
        connection.execute("SET checkpoint_threshold = '1TB'")
        version = duckdb.__version__
        settings = "Python DuckDB; threads=1; memory_limit=512MB (not RSS cap); default durable WAL; explicit checkpoints; SQL INSERT (not appender/COPY)"
    else:
        connection = sqlite3.connect(str(directory / "data.sqlite"), isolation_level=None, cached_statements=0)
        for q in ["PRAGMA journal_mode=WAL", "PRAGMA synchronous=FULL", "PRAGMA wal_autocheckpoint=0",
                  f"PRAGMA cache_size=-{w['cache_mib'] * 1024}", "PRAGMA mmap_size=0", "PRAGMA foreign_keys=ON"]:
            connection.execute(q).fetchall()
        version = sqlite3.sqlite_version
        settings = "Python native SQLite; WAL synchronous=FULL; auto-checkpoint off; mmap off; cached_statements=0; autocommit per multi-row INSERT"

    def checkpoint():
        q = "CHECKPOINT" if args.engine == "duckdb" else "PRAGMA wal_checkpoint(TRUNCATE)"
        result = connection.execute(q).fetchall()
        if args.engine != "duckdb" and result[0][0] != 0:
            raise RuntimeError("SQLite checkpoint busy")

    result = dict(engine=args.engine, version=version, rows=w["rows"], settings=settings,
                  phases=[], queries=[], plans={}, files={})
    try:
        start = time.perf_counter_ns()
        for q in w["schema"]:
            connection.execute(q).fetchall()
        result["phases"].append(dict(name="schema", ms=(time.perf_counter_ns() - start) / 1e6))
        insert_ns, checkpoint_ns, loaded, since = 0, 0, 0, 0
        start = time.perf_counter_ns()
        with open(w["inserts"], encoding="utf-8") as source:
            for line in source:
                tick = time.perf_counter_ns()
                connection.execute(line).fetchall()
                insert_ns += time.perf_counter_ns() - tick
                count = min(w["batch"], w["rows"] - loaded)
                loaded += count
                since += count
                if since >= w["checkpoint_rows"]:
                    tick = time.perf_counter_ns()
                    checkpoint()
                    checkpoint_ns += time.perf_counter_ns() - tick
                    since = 0
        if loaded != w["rows"]:
            raise AssertionError("fixture row mismatch")
        tick = time.perf_counter_ns()
        checkpoint()
        checkpoint_ns += time.perf_counter_ns() - tick
        result["phases"].extend([
            dict(name="ingest_wall", ms=(time.perf_counter_ns() - start) / 1e6),
            dict(name="insert_sql", ms=insert_ns / 1e6), dict(name="checkpoints", ms=checkpoint_ns / 1e6)])
        for case in w["queries"]:
            prefix = "EXPLAIN " if args.engine == "duckdb" else "EXPLAIN QUERY PLAN "
            result["plans"][case["name"]] = connection.execute(prefix + case["sql"]).fetchall()
            result["queries"].append(measure(connection, case, w["repetitions"], w["warmups"], args.engine))
        start = time.perf_counter_ns()
        connection.execute(w["update"]).fetchall()
        result["phases"].append(dict(name="single_update", ms=(time.perf_counter_ns() - start) / 1e6))
        case = dict(w["after_update"], name="aggregate_after_update")
        result["queries"].append(measure(connection, case, 1, 0, args.engine))
        result["files"] = {str(p.relative_to(directory)): p.stat().st_size
                           for p in directory.rglob("*") if p.is_file()}
    finally:
        connection.close()
    return result


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--engine", choices=["duckdb", "sqlite-native"], required=True)
    parser.add_argument("--directory", required=True)
    parser.add_argument("--workload", required=True)
    print(json.dumps(run(parser.parse_args()), allow_nan=False))
