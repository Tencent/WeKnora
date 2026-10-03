"""Verify a ParadeDB patch upgrade using disposable Docker volumes and real migrations.

Requires Python 3 and Docker; no database credentials or running WeKnora needed.
Run: python3 scripts/test_paradedb_upgrade.py
Artifacts (snapshots, plans, logs and a synthetic-data backup) are kept in /tmp.
"""
import argparse
import hashlib
import json
import pathlib
import random
import subprocess
import tempfile
import time
import uuid

ROOT = pathlib.Path(__file__).resolve().parents[1]
OUT = None
CONTAINER = 'weknora-pdb-upgrade-' + uuid.uuid4().hex[:12]

def sql(query, db='weknora'):
    p = subprocess.run(['docker', 'exec', '-i', CONTAINER, 'psql', '-X', '-qAt', '-U', 'postgres', '-d', db, '-v', 'ON_ERROR_STOP=1'], input=query, text=True, capture_output=True)
    if p.returncode:
        raise RuntimeError(p.stderr)
    return p.stdout.strip()

def quote(s):
    return "'" + s.replace("'", "''") + "'"

def seed():
    rng = random.Random(221226)
    topics = ['数据库 向量 检索 PostgreSQL database search', '知识库 文档 问答 人工智能 retrieval', '北京 上海 深圳 中国 城市', 'Docker 安装 部署 CPU AVX2', '备份 恢复 升级 数据完整性', '权限 租户 共享 用户 authentication']
    stmts = ['BEGIN;', 'TRUNCATE embeddings, tenants, sessions, messages, models, knowledge_bases, knowledges, chunks RESTART IDENTITY CASCADE;']
    queries = {}
    for dim in (798, 1024, 3584):
        for n in range(240):
            vector = '[' + ','.join(f'{rng.uniform(-1, 1):.5f}' for _ in range(dim)) + ']'
            if n in (1, 71, 139):
                queries[f'{dim}_{n}'] = vector
            # Vary term frequency/length; cover NULL, disabled, tags, KB and document filters.
            content = ' '.join([topics[n % 6]] * (1 + n % 7)) + ' ' + ' '.join(rng.choices(topics, k=n % 5))
            enabled = 'NULL' if n % 13 == 0 else ('false' if n % 11 == 0 else 'true')
            stmts.append(f"INSERT INTO embeddings(source_id, source_type, chunk_id, knowledge_id, knowledge_base_id, tag_id, content, dimension, embedding, is_enabled) VALUES ('src-{dim}-{n}',{n % 3},'chunk-{dim}-{n}','doc-{n % 9}','kb-{n % 3}',{quote('tag-'+str(n % 4)) if n % 7 else 'NULL'},{quote(content)},{dim},{quote(vector)},{enabled});")
    stmts += [
        "INSERT INTO tenants(id,name,description,business) VALUES (10101,'升级验证','中文与 emoji 🧪','test');",
        "INSERT INTO sessions(id,tenant_id,title) VALUES ('upgrade-session',10101,'升级前会话');",
        "INSERT INTO messages(id,request_id,session_id,role,content,knowledge_references) VALUES ('upgrade-message','upgrade-request','upgrade-session','assistant','原始回答：数据库检索 🧪','[{\"id\":\"source\",\"score\":0.875}]');",
        "INSERT INTO models(id,tenant_id,name,type,source,parameters) VALUES ('upgrade-model',10101,'fixture','Embedding','remote','{\"dimension\":1024}');",
        "INSERT INTO knowledge_bases(id,tenant_id,name,embedding_model_id,summary_model_id) VALUES ('kb-0',10101,'验证知识库','upgrade-model','');",
        "INSERT INTO knowledges(id,tenant_id,knowledge_base_id,type,title,source,metadata) VALUES ('doc-0',10101,'kb-0','file','中文文档','upload','{\"nested\":{\"value\":42}}');",
        "INSERT INTO chunks(id,tenant_id,knowledge_base_id,knowledge_id,content,chunk_index,start_at,end_at) VALUES ('upgrade-chunk',10101,'kb-0','doc-0','原始分块',0,0,4);",
        'COMMIT; VACUUM ANALYZE embeddings;',
    ]
    (OUT/'seed.sql').write_text('\n'.join(stmts))
    sql('\n'.join(stmts))
    (OUT/'vectors.json').write_text(json.dumps(queries))
    print('Seeded 720 embeddings plus tenant/model/KB/document/chunk/session/message data', flush=True)

def snapshot(stage):
    tables = sql("SELECT c.relname FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relkind IN ('r','p') AND NOT EXISTS (SELECT 1 FROM pg_depend d WHERE d.classid='pg_class'::regclass AND d.objid=c.oid AND d.deptype='e') ORDER BY 1").splitlines()
    data = {}
    for table in tables:
        ident = '"'+table.replace('"','""')+'"'
        rows = sql(f'SELECT to_jsonb(t)::text FROM {ident} t ORDER BY to_jsonb(t)::text')
        data[table] = {'count': int(sql(f'SELECT count(*) FROM {ident}')), 'sha256': hashlib.sha256(rows.encode()).hexdigest()}
    seq = sql("SELECT schemaname,sequencename,start_value,min_value,max_value,increment_by,cycle,last_value FROM pg_sequences WHERE schemaname='public' ORDER BY sequencename")
    schema = sql("SELECT table_name,column_name,data_type,udt_name,is_nullable,column_default FROM information_schema.columns WHERE table_schema='public' ORDER BY table_name,ordinal_position")
    indexes = sql("SELECT c.relname,i.indisvalid,i.indisready,c.relfilenode,pg_get_indexdef(i.indexrelid) FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid JOIN pg_class t ON t.oid=i.indrelid JOIN pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname='public' ORDER BY c.relname")
    results = {}
    plans = {}
    keyword_queries = ['数据库', '知识库 文档', 'Docker AVX2', 'PostgreSQL database', '北京 上海', '权限 用户', '不存在的关键词xyz', '']
    filters = ['', " AND knowledge_base_id IN ('kb-0','kb-1')", " AND knowledge_base_id='kb-0' AND knowledge_id IN ('doc-0','doc-3')", " AND tag_id IN ('tag-1','tag-2')"]
    # Same |||, scoring and enabled predicate as KeywordsRetrieve. id only resolves equal-score ties.
    for q in keyword_queries:
        for j, filt in enumerate(filters):
            query = f"SELECT id,paradedb.score(id) AS score FROM embeddings WHERE content ||| {quote(q)} AND (is_enabled IS NULL OR is_enabled=true){filt} ORDER BY score DESC,id LIMIT 10"
            key = f'bm25:{q}:{j}'
            results[key] = sql(query)
            plans[key] = sql('EXPLAIN (COSTS OFF) '+query)
    vectors = json.loads((OUT/'vectors.json').read_text())
    for name, vector in vectors.items():
        dim = int(name.split('_')[0])
        for j, filt in enumerate(filters):
            # Exact production candidate expansion, halfvec expression, threshold, and ef_search.
            query = f"SELECT id,1-distance AS score FROM (SELECT id,embedding::halfvec({dim}) <=> {quote(vector)}::halfvec({dim}) AS distance FROM embeddings WHERE dimension={dim} AND (is_enabled IS NULL OR is_enabled=true){filt} ORDER BY embedding::halfvec({dim}) <=> {quote(vector)}::halfvec({dim}) LIMIT 100) candidates WHERE distance<=1.1 ORDER BY distance LIMIT 10"
            key = f'vector:{name}:{j}'
            results[key] = sql('BEGIN; SET LOCAL hnsw.ef_search=100; '+query+'; COMMIT;')
            # Force the index separately so a small fixture cannot silently test only sequential scans.
            results[key+':hnsw'] = sql('BEGIN; SET LOCAL enable_seqscan=off; SET LOCAL enable_bitmapscan=off; SET LOCAL enable_sort=off; SET LOCAL hnsw.ef_search=100; '+query+'; COMMIT;')
            plans[key] = sql('BEGIN; SET LOCAL enable_seqscan=off; SET LOCAL enable_bitmapscan=off; SET LOCAL enable_sort=off; SET LOCAL hnsw.ef_search=100; EXPLAIN (COSTS OFF) '+query+'; COMMIT;')
            assert 'Index Scan using embeddings_embedding_idx_'+str(dim) in plans[key], key+' did not use HNSW'
    assert any('Custom Scan' in p for k,p in plans.items() if k.startswith('bm25:')), 'BM25 index was not used'
    result = {'tables': data, 'sequences': seq, 'schema': schema, 'indexes': indexes, 'queries': results}
    (OUT/(stage+'.json')).write_text(json.dumps(result, ensure_ascii=False, indent=2))
    (OUT/(stage+'-plans.json')).write_text(json.dumps(plans, ensure_ascii=False, indent=2))
    (OUT/(stage+'-versions.txt')).write_text(sql("SELECT version(); SELECT extname,extversion FROM pg_extension ORDER BY 1; SELECT * FROM paradedb.version_info();"))
    print(f'{stage}: {len(data)} tables, {len(results)} retrieval queries captured', flush=True)
    if stage != 'before':
        baseline = json.loads((OUT/'before.json').read_text())
        differences = [key for key in result if result[key] != baseline[key]]
        if differences:
            print('Differences:', differences)
            if 'queries' in differences:
                print([key for key,value in results.items() if baseline['queries'][key] != value])
            raise SystemExit(1)
        print('PASS: rows, columns, sequences, index files, result IDs, ordering and scores unchanged', flush=True)

def docker(*args, **kwargs):
    return subprocess.run(['docker', *args], check=True, capture_output=True, **kwargs)


def start(image, volume):
    docker('run', '-d', '--name', CONTAINER, '--network', 'none', '--shm-size=512m',
           '-e', 'POSTGRES_PASSWORD=isolated-upgrade-test', '-e', 'POSTGRES_DB=weknora',
           '-v', volume+':/var/lib/postgresql/data', image)
    for _ in range(120):
        # Wait for the final server, not the bootstrap server that only uses a socket.
        ready = subprocess.run(['docker', 'exec', CONTAINER, 'pg_isready', '-h', '127.0.0.1', '-U', 'postgres', '-d', 'weknora'], capture_output=True)
        if ready.returncode == 0:
            return
        time.sleep(1)
    raise RuntimeError('Postgres did not start: '+docker('logs', CONTAINER).stdout.decode())


def migrate(db, paths, label):
    with (OUT/(label+'.log')).open('w') as log:
        for path in paths:
            log.write(path.name+'\n')
            sql('BEGIN;\n'+path.read_text()+'\nCOMMIT;', db)
    print(f'{label}: {len(paths)} SQL migrations passed', flush=True)


def check_writes():
    # Committed writes must update both existing BM25 and HNSW indexes.
    vector = '['+','.join(['1']*1024)+']'
    new_id = sql("INSERT INTO embeddings(source_id,source_type,chunk_id,knowledge_id,knowledge_base_id,content,dimension,embedding,is_enabled) VALUES ('post-upgrade',0,'new-chunk','new-doc','new-kb','upgradeuniquetoken',1024,"+quote(vector)+",true) RETURNING id")
    assert sql("SELECT id FROM embeddings WHERE content ||| 'upgradeuniquetoken'") == new_id
    nearest = sql("BEGIN; SET LOCAL enable_seqscan=off; SET LOCAL enable_bitmapscan=off; SET LOCAL enable_sort=off; SELECT id FROM embeddings WHERE dimension=1024 ORDER BY embedding::halfvec(1024) <=> "+quote(vector)+"::halfvec(1024) LIMIT 1; COMMIT;")
    assert nearest == new_id, 'HNSW did not find the newly inserted vector'
    sql("UPDATE embeddings SET content='updateduniquetoken' WHERE id="+new_id)
    assert sql("SELECT count(*) FROM embeddings WHERE content ||| 'upgradeuniquetoken'") == '0'
    assert sql("SELECT id FROM embeddings WHERE content ||| 'updateduniquetoken'") == new_id
    sql('DELETE FROM embeddings WHERE id='+new_id)
    assert sql("SELECT count(*) FROM embeddings WHERE content ||| 'updateduniquetoken'") == '0'
    print('PASS: committed insert/vector lookup/update/delete on existing indexes', flush=True)


def main():
    global OUT
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--output-dir', type=pathlib.Path)
    args = parser.parse_args()
    OUT = args.output_dir or pathlib.Path(tempfile.mkdtemp(prefix='weknora-pdb-upgrade-'))
    OUT.mkdir(parents=True, exist_ok=True)
    if any(OUT.iterdir()):
        raise RuntimeError('Use an empty output directory')
    print('Evidence: '+str(OUT), flush=True)
    volume = CONTAINER+'-data'
    old, new = 'paradedb/paradedb:v0.22.2-pg17', 'paradedb/paradedb:v0.22.6-pg17'
    paths = sorted((ROOT/'migrations/versioned').glob('*.up.sql'))
    baseline = [p for p in paths if int(p.name.split('_')[0]) < 99]
    upgrade = ROOT/'migrations/versioned/000099_pg_search_0226.up.sql'
    # Only resources created by this invocation are ever removed.
    docker('volume', 'create', volume)
    try:
        start(old, volume)
        migrate('weknora', baseline, 'old-migrations')
        assert sql("SELECT extversion FROM pg_extension WHERE extname='pg_search'") == '0.22.2'
        # Older external servers without the target package must remain usable.
        migrate('weknora', [upgrade], 'unavailable-target')
        assert sql("SELECT extversion FROM pg_extension WHERE extname='pg_search'") == '0.22.2'
        seed()
        snapshot('before')
        with (OUT/'before.dump').open('wb') as backup:
            subprocess.run(['docker', 'exec', CONTAINER, 'pg_dump', '-U', 'postgres', '-d', 'weknora', '-Fc'], check=True, stdout=backup)
        docker('stop', '-t', '60', CONTAINER)
        docker('rm', CONTAINER)
        start(new, volume)
        # Replacing the image alone leaves the SQL catalog at the old version.
        assert sql("SELECT extversion FROM pg_extension WHERE extname='pg_search'") == '0.22.2'
        sql("BEGIN; SET LOCAL app.skip_embedding='true';\n"+upgrade.read_text()+"\nCOMMIT;")
        assert sql("SELECT extversion FROM pg_extension WHERE extname='pg_search'") == '0.22.2'
        migrate('weknora', [upgrade, upgrade], 'extension-upgrade-and-idempotence')
        assert sql("SELECT extversion FROM pg_extension WHERE extname='pg_search'") == '0.22.6'
        assert sql('SELECT version FROM paradedb.version_info()') == '0.22.6'
        snapshot('after')
        docker('restart', '-t', '60', CONTAINER)
        for _ in range(60):
            try:
                sql('SELECT 1')
                break
            except RuntimeError:
                time.sleep(1)
        snapshot('after-restart')
        check_writes()
        sql('CREATE DATABASE upgrade_fresh TEMPLATE template0')
        # The new migration must not install pg_search on non-search databases.
        migrate('upgrade_fresh', [upgrade], 'absent-extension')
        assert sql("SELECT count(*) FROM pg_extension WHERE extname='pg_search'", 'upgrade_fresh') == '0'
        migrate('upgrade_fresh', paths, 'fresh-migrations')
        assert sql("SELECT extversion FROM pg_extension WHERE extname='pg_search'", 'upgrade_fresh') == '0.22.6'
        sql("INSERT INTO embeddings(source_id,source_type,content,dimension) VALUES ('fresh',0,'新安装检索',1024)", 'upgrade_fresh')
        assert sql("SELECT count(*) FROM embeddings WHERE content ||| '检索'", 'upgrade_fresh') == '1'
        print('PASS: fresh install and Chinese BM25 query', flush=True)
        (OUT/'PASS').write_text('Upgrade, restart, data integrity, retrieval, writes and fresh install passed.\n')
    finally:
        logs = subprocess.run(['docker', 'logs', CONTAINER], capture_output=True)
        (OUT/'postgres.log').write_bytes(logs.stdout+logs.stderr)
        subprocess.run(['docker', 'rm', '-f', CONTAINER], capture_output=True)
        docker('volume', 'rm', volume)


if __name__ == '__main__':
    main()
