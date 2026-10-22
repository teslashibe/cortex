#!/usr/bin/env python3
"""Optional loopback ONNX embedding adapter with exact, lossless input checks."""
import argparse
import hashlib
import json
from http.server import BaseHTTPRequestHandler, HTTPServer
from pathlib import Path


def segments(text, count, limit, width=512, overlap=128):
    """Offsets are Python Unicode code points in the original string."""
    if not isinstance(text, str) or not text or not 1 <= overlap < width:
        raise ValueError('invalid segmentation input')
    output, start = [], 0
    while start < len(text):
        end = min(len(text), start + width)
        # Token counts need not be monotone under substring extension. Shrink
        # until a checked prefix fits; maximal token packing is not required.
        while end > start and count(text[start:end]) > limit:
            end = start + (end - start) * 3 // 4
        if output and end <= output[-1]['end_rune']:
            # A dense token region may not fit the requested overlap. Preserve
            # coverage and progress by starting at the previous end instead.
            start = output[-1]['end_rune']
            continue
        if end == start:
            raise ValueError('a code point exceeds the token budget')
        tokens = count(text[start:end])
        if not 1 <= tokens <= limit:
            raise ValueError('invalid token count')
        output.append({'start_rune': start, 'end_rune': end, 'tokens': tokens})
        if end == len(text):
            break
        start = end - min(overlap, (end - start) // 4)
    return output


def checked_texts(texts, count, limit):
    if not isinstance(texts, list) or not 1 <= len(texts) <= 32:
        raise ValueError('invalid batch size')
    if any(not isinstance(t, str) or not t or count(t) > limit for t in texts):
        raise ValueError('input exceeds the encoder token budget')
    return texts


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--model-dir', required=True)
    parser.add_argument('--model-id', required=True, help='immutable model/revision identifier')
    parser.add_argument('--max-tokens', type=int, default=256)
    parser.add_argument('--port', type=int, default=0)
    args = parser.parse_args()
    if not 4 <= args.max_tokens <= 65536:
        parser.error('invalid token budget')
    import numpy as np
    import onnxruntime as ort
    from tokenizers import Tokenizer
    root = Path(args.model_dir).resolve()
    tokenizer = Tokenizer.from_file(str(root / 'tokenizer.json'))
    tokenizer.no_truncation()
    tokenizer.no_padding()
    count = lambda text: len(tokenizer.encode(text).ids)
    tokenizer_hash = hashlib.sha256((root / 'tokenizer.json').read_bytes()).hexdigest()
    model_hash = hashlib.sha256((root / 'onnx/model.onnx').read_bytes()).hexdigest()
    identity = f'{args.model_id}|onnx={model_hash}|tokenizer={tokenizer_hash}|limit={args.max_tokens}'
    segmenter = f'{identity}|segments=v1:512:128'
    options = ort.SessionOptions()
    options.intra_op_num_threads = 2
    options.inter_op_num_threads = 1
    session = ort.InferenceSession(str(root / 'onnx/model.onnx'), sess_options=options,
                                   providers=['CPUExecutionProvider'])
    class Handler(BaseHTTPRequestHandler):
        def log_message(self, *_):
            pass
        def do_POST(self):
            self.connection.settimeout(30)
            try:
                n = int(self.headers.get('Content-Length', 0))
                if not 0 < n <= 1048576:
                    raise ValueError('invalid body size')
                body = json.loads(self.rfile.read(n))
                if self.path == '/v1/segment':
                    rows = body['inputs']
                    if body['segmenter'] != segmenter or body['max_tokens'] != args.max_tokens or not 1 <= len(rows) <= 32:
                        raise ValueError('segmentation contract mismatch')
                    result = {'segmenter': segmenter, 'max_tokens': args.max_tokens,
                              'results': [{'id': r['id'], 'windows': segments(r['text'], count, args.max_tokens)} for r in rows]}
                elif self.path == '/v1/embeddings':
                    if body.get('model') != identity:
                        raise ValueError('model identity mismatch')
                    texts = checked_texts(body['input'], count, args.max_tokens)
                    batch = tokenizer.encode_batch(texts)
                    length = max(len(x.ids) for x in batch)
                    pad_id = tokenizer.token_to_id('[PAD]')
                    if pad_id is None:
                        raise ValueError('this adapter requires a [PAD] token')
                    feed = {'input_ids': np.array([x.ids + [pad_id] * (length-len(x.ids)) for x in batch], dtype=np.int64),
                            'attention_mask': np.array([[1] * len(x.ids) + [0] * (length-len(x.ids)) for x in batch], dtype=np.int64),
                            'token_type_ids': np.array([x.type_ids + [0] * (length-len(x.ids)) for x in batch], dtype=np.int64)}
                    feed = {v.name: feed[v.name] for v in session.get_inputs()}
                    hidden = session.run(None, feed)[0]
                    mask = feed['attention_mask'][..., None]
                    pooled = (hidden * mask).sum(1) / np.clip(mask.sum(1), 1, None)
                    pooled /= np.linalg.norm(pooled, axis=1, keepdims=True)
                    if not np.isfinite(pooled).all():
                        raise ValueError('invalid encoder output')
                    result = {'model': identity, 'data': [{'index': i, 'embedding': v.tolist()} for i, v in enumerate(pooled)]}
                else:
                    raise ValueError('invalid path')
                raw = json.dumps(result).encode()
                self.send_response(200)
                self.send_header('Content-Type', 'application/json')
                self.send_header('Content-Length', str(len(raw)))
                self.end_headers()
                self.wfile.write(raw)
            except Exception:
                self.send_response(400)
                self.end_headers()
    server = HTTPServer(('127.0.0.1', args.port), Handler)
    print(json.dumps({'port': server.server_port, 'model': identity, 'segmenter': segmenter,
                      'max_tokens': args.max_tokens}), flush=True)
    server.serve_forever()


if __name__ == '__main__':
    main()
