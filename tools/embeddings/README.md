# Exact tokenizer adapter

This optional local adapter pairs ONNX embeddings with source segmentation using
the same tokenizer. It supports the mean-pooled, normalized BERT output used by
`sentence-transformers/all-MiniLM-L6-v2`. It is not a general ONNX model server.
Install `requirements.txt` in a separate Python environment. Supply a local model
directory containing `tokenizer.json` and `onnx/model.onnx`; weights are not bundled.

```sh
python tools/embeddings/server.py --model-dir "$MODEL_DIRECTORY" \
  --model-id sentence-transformers/all-MiniLM-L6-v2@1110a243fdf4706b3f48f1d95db1a4f5529b4d41
```

The startup JSON contains `port`, `model`, `segmenter`, and `max_tokens`. Configure
`CORTEX_EMBEDDING_URL=http://127.0.0.1:PORT/v1`, copy `model` into
`CORTEX_EMBEDDING_MODEL`, and set `CORTEX_SEGMENTER_URL` to
`http://127.0.0.1:PORT/v1/segment`. Copy `segmenter` and `max_tokens` into
`CORTEX_SEGMENTER_ID` and `CORTEX_SEGMENTER_MAX_TOKENS`.

Both model files are hashed into the identity. Changing weights, tokenizer, token
limit, or segmentation policy therefore creates a separate vector namespace.
Segmentation retains original Unicode offsets and covers all source text. It
reduces overlap when dense text would prevent progress. Every embedding request
is checked again before inference. Oversized queries are rejected as well.

The server accepts only loopback requests, logs no source text, and processes one
request at a time. It has no authentication. Cortex's 100,000-window and
20,000-observation limits still apply; a local exact scan is intended for bounded
personal memory collections. Segmentation is repeated during recall, so a remote
segmenter also receives sources during recall. Use the same data authorization as
for explicit indexing. This adapter does not download or upload any model or data.
