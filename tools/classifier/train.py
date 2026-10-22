#!/usr/bin/env python3
"""Private offline classifier training and loopback inference; never uploads data."""
import argparse
import hashlib
import importlib.metadata
import json
import os
from pathlib import Path
import time

os.environ.setdefault('HF_HUB_DISABLE_TELEMETRY', '1')
os.environ.setdefault('TOKENIZERS_PARALLELISM', 'false')
os.environ.setdefault('WANDB_DISABLED', 'true')
os.umask(0o077)
POLICY = 'cortex-candidate-v1'
LABELS = {'retention': ['durable', 'temporary', 'ignore', 'uncertain'],
          'kind': ['fact', 'behavior', 'preference', 'relationship', 'project', 'routine']}


def private_path(path):
    path = Path(path).expanduser().resolve()
    if any((p / '.git').exists() for p in [path, *path.parents]):
        raise ValueError('datasets and model output must be outside a Git checkout')
    return path


def load_rows(path, allow_provisional=False):
    rows = [json.loads(line) for line in private_path(path).read_text().splitlines() if line.strip()]
    groups, hashes, ids = {}, {}, set()
    for r in rows:
        if not r.get('id') or r['id'] in ids:
            raise ValueError('missing or duplicate example ID')
        ids.add(r['id'])
        if r.get('split') not in ('train', 'dev', 'test') or not r.get('group'):
            raise ValueError('explicit train/dev/test split and group required')
        if not isinstance(r.get('context', r.get('text')), str) or len(r.get('context', r.get('text', ''))) > 1024:
            raise ValueError('context must contain at most 1024 code points')
        if not isinstance(r.get('text'), str) or not r['text'].strip() or len(r['text']) > 512:
            raise ValueError('candidate must contain 1..512 code points')
        if r.get('label_source') not in ('human', 'synthetic', 'teacher'):
            raise ValueError('label_source must be human, synthetic, or teacher')
        if r['label_source'] == 'teacher' and not allow_provisional:
            raise ValueError('teacher labels require --allow-provisional; they are not gold labels')
        if r.get('retention') not in LABELS['retention']:
            raise ValueError('invalid retention label')
        if r.get('kind') not in LABELS['kind'] and r.get('kind') is not None:
            raise ValueError('invalid kind label')
        if r['retention'] in ('durable', 'temporary') and r.get('kind') is None:
            raise ValueError('retained candidates require kind')
        digest = hashlib.sha256(' '.join(r['text'].lower().split()).encode()).hexdigest()
        for mapping, key in ((groups, r['group']), (hashes, digest)):
            if key in mapping and mapping[key] != r['split']:
                raise ValueError('conversation or exact normalized text leaked across splits')
            mapping[key] = r['split']
    if set(r['split'] for r in rows) != {'train', 'dev', 'test'}:
        raise ValueError('all three splits required')
    return rows


def input_text(row):
    # Candidate first; preserve quote/negation context. This is the model contract.
    context = row.get('context') or row['text']
    return 'Candidate: ' + row['text'] + '\nContext: ' + context


def probabilities(vectors, head):
    import numpy as np
    z = (vectors @ np.array(head['coef']).T + np.array(head['intercept'])) / head['temperature']
    z -= z.max(axis=1, keepdims=True)
    p = np.exp(z)
    return p / p.sum(axis=1, keepdims=True)


def metrics(y, p, names):
    import numpy as np
    from sklearn.metrics import accuracy_score, f1_score, confusion_matrix
    prediction = p.argmax(axis=1)
    confidence = p.max(axis=1)
    accepted = confidence >= .8
    truth = np.eye(len(names))[y]
    ece = 0.
    for lo in np.linspace(0, .9, 10):
        mask = (confidence >= lo) & (confidence < lo + .1 + (1e-10 if lo > .89 else 0))
        if mask.any():
            ece += mask.mean() * abs((prediction[mask] == y[mask]).mean() - confidence[mask].mean())
    return {'n': len(y), 'accuracy': float(accuracy_score(y, prediction)),
            'macro_f1': float(f1_score(y, prediction, labels=list(range(len(names))), average='macro', zero_division=0)),
            'brier': float(((p-truth)**2).sum(axis=1).mean()), 'ece_10_bins': float(ece),
            'coverage_at_0.8': float(accepted.mean()),
            'error_at_0.8': float((prediction[accepted] != y[accepted]).mean()) if accepted.any() else None,
            'labels': names, 'confusion': confusion_matrix(y, prediction, labels=list(range(len(names)))).tolist()}


def train(args):
    import numpy as np
    import torch
    from sentence_transformers import SentenceTransformer
    from sklearn.linear_model import LogisticRegression
    torch.set_num_threads(2)
    rows = load_rows(args.data, args.allow_provisional)
    out = private_path(args.output)
    out.mkdir(parents=True, exist_ok=False)
    started = time.monotonic()
    if args.backend == 'setfit':
        from datasets import Dataset
        from setfit import SetFitModel, Trainer, TrainingArguments
        selected = [r for r in rows if r['split'] == 'train' and r.get('kind') is not None]
        model = SetFitModel.from_pretrained(args.encoder, revision=args.revision, device='cpu')
        dataset = Dataset.from_dict({'text': [input_text(r) for r in selected],
                                     'label': [LABELS['kind'].index(r['kind']) for r in selected]})
        config = TrainingArguments(batch_size=16, num_epochs=1, max_steps=args.steps,
                                   num_iterations=5, seed=17, report_to='none', use_amp=False,
                                   output_dir=str(out/'trainer'), logging_dir=str(out/'logs'), save_strategy='no')
        Trainer(model=model, args=config, train_dataset=dataset).train()
        encoder = model.model_body
    else:
        encoder = SentenceTransformer(args.encoder, revision=args.revision, device='cpu')
    training_device = str(encoder.device)
    encoder.to('cpu')
    encoder.max_seq_length = 256
    encoder.save(str(out / 'encoder'))
    vectors = encoder.encode([input_text(r) for r in rows], batch_size=32,
                             normalize_embeddings=True, show_progress_bar=False)
    heads, report = {}, {}
    for name, labels in LABELS.items():
        selected = {split: [i for i, r in enumerate(rows) if r['split'] == split and r.get(name) is not None]
                    for split in ('train', 'dev', 'test')}
        y = {split: np.array([labels.index(rows[i][name]) for i in indices]) for split, indices in selected.items()}
        if set(y['train']) != set(range(len(labels))) or any(len(x) == 0 for x in y.values()):
            raise ValueError('each training head needs every class and nonempty dev/test data')
        clf = LogisticRegression(C=10., max_iter=1500, random_state=17).fit(vectors[selected['train']], y['train'])
        head = {'coef': clf.coef_.tolist(), 'intercept': clf.intercept_.tolist(), 'temperature': 1.}
        # Freeze temperature on dev; never use test labels for model selection.
        candidates = []
        for t in [.5, .75, 1., 1.5, 2., 3., 5.]:
            head['temperature'] = t
            p = probabilities(vectors[selected['dev']], head)
            candidates.append((float(-np.log(p[np.arange(len(y['dev'])), y['dev']] + 1e-15).mean()), t))
        head['temperature'] = min(candidates)[1]
        heads[name] = head
        report[name] = {split: metrics(y[split], probabilities(vectors[idx], head), labels)
                        for split, idx in selected.items()}
    (out / 'heads.json').write_text(json.dumps(heads, indent=2))
    file_hashes = {str(p.relative_to(out)): hashlib.sha256(p.read_bytes()).hexdigest()
                   for p in sorted(out.rglob('*')) if p.is_file()}
    identity = hashlib.sha256(json.dumps(file_hashes, sort_keys=True).encode()).hexdigest()
    manifest = {'model': 'cortex-' + args.backend + '-' + identity[:16], 'policy_version': POLICY,
                'backend': args.backend, 'training_device': training_device, 'base_encoder': args.encoder, 'base_revision': args.revision,
                'labels': LABELS, 'files': file_hashes, 'seed': 17, 'steps': args.steps if args.backend == 'setfit' else 0,
                'training_seconds': time.monotonic()-started,
                'dataset_sha256': hashlib.sha256(Path(args.data).read_bytes()).hexdigest(),
                'label_sources': {source: sum(r['label_source'] == source for r in rows)
                                  for source in ('human', 'synthetic', 'teacher')},
                'splits': {split: sum(r['split'] == split for r in rows) for split in ('train', 'dev', 'test')},
                'packages': {name: importlib.metadata.version(name) for name in ('torch','transformers','sentence-transformers','scikit-learn','setfit')},
                'promotion': 'shadow_only', 'provisional_labels': any(r['label_source'] == 'teacher' for r in rows)}
    (out / 'manifest.json').write_text(json.dumps(manifest, indent=2))
    (out / 'metrics.json').write_text(json.dumps(report, indent=2))
    print(json.dumps({'model': manifest['model'], 'training_seconds': manifest['training_seconds'], 'metrics': report}))


def serve(args):
    from http.server import BaseHTTPRequestHandler, HTTPServer
    from sentence_transformers import SentenceTransformer
    import torch
    torch.set_num_threads(2)
    root = private_path(args.model)
    manifest = json.loads((root/'manifest.json').read_text())
    for name, digest in manifest['files'].items():
        file = (root/name).resolve()
        if not file.is_relative_to(root) or hashlib.sha256(file.read_bytes()).hexdigest() != digest:
            raise ValueError('model integrity mismatch')
    heads = json.loads((root/'heads.json').read_text())
    encoder = SentenceTransformer(str(root/'encoder'), device='cpu', local_files_only=True)
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass
        def do_POST(self):
            try:
                if self.path != '/classify':
                    raise ValueError('invalid path')
                n = int(self.headers.get('Content-Length', 0))
                if not 0 < n <= 1048576:
                    raise ValueError('invalid body size')
                request = json.loads(self.rfile.read(n))
                rows = request['inputs']
                if request['model'] != manifest['model'] or request['policy_version'] != POLICY or not 1 <= len(rows) <= 128:
                    raise ValueError('model, policy or batch mismatch')
                if any(not isinstance(r.get('text'), str) or not 0 < len(r['text']) <= 512 or len(r.get('context','')) > 1024 for r in rows):
                    raise ValueError('invalid candidate')
                vectors = encoder.encode([input_text(r) for r in rows], batch_size=32, normalize_embeddings=True)
                distributions = {name: probabilities(vectors, head) for name, head in heads.items()}
                results = [{'id': row['id'], **{name: dict(zip(LABELS[name], p[i].tolist()))
                           for name, p in distributions.items()}} for i, row in enumerate(rows)]
                payload = json.dumps({'model': manifest['model'], 'policy_version': POLICY, 'predictions': results}).encode()
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.end_headers()
                self.wfile.write(payload)
            except Exception:
                self.send_response(400)
                self.end_headers()
    server = HTTPServer(('127.0.0.1', args.port), Handler)
    print(json.dumps({'port': server.server_port, 'model': manifest['model']}), flush=True)
    server.serve_forever()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    sub = parser.add_subparsers(dest='command', required=True)
    p = sub.add_parser('train')
    p.add_argument('--data', required=True)
    p.add_argument('--output', required=True)
    p.add_argument('--backend', choices=['linear', 'setfit'], default='linear')
    p.add_argument('--encoder', default='sentence-transformers/all-MiniLM-L6-v2')
    p.add_argument('--revision', default='1110a243fdf4706b3f48f1d95db1a4f5529b4d41')
    p.add_argument('--steps', type=int, default=80)
    p.add_argument('--allow-provisional', action='store_true')
    p = sub.add_parser('serve')
    p.add_argument('--model', required=True)
    p.add_argument('--port', type=int, default=0)
    args = parser.parse_args()
    train(args) if args.command == 'train' else serve(args)


if __name__ == '__main__':
    main()
