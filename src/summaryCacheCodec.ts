// Bound both disk usage and decompressed size. Large complete account snapshots
// compress well; rejecting them by raw JSON size made every reload start cold.
const maxRawBytes = 128_000_000;
const maxStoredBytes = 8_000_000;
export async function encodeSummaryCache(value: unknown): Promise<{summary?: unknown; gzip?: ArrayBuffer} | undefined> {
  const json = JSON.stringify(value);
  if (json.length > maxRawBytes) return;
  if (json.length <= maxStoredBytes) return {summary: value};
  if (typeof CompressionStream === "undefined") return;
  const bytes = new TextEncoder().encode(json);
  if (bytes.byteLength > maxRawBytes) return;
  const input = new ReadableStream<BufferSource>({start(c) { c.enqueue(bytes); c.close(); }});
  const gzip = await new Response(input.pipeThrough(new CompressionStream("gzip"))).arrayBuffer();
  return gzip.byteLength <= maxStoredBytes ? {gzip} : undefined;
}
export async function decodeSummaryCache(record: {summary?: unknown; gzip?: ArrayBuffer}): Promise<unknown> {
  if (record.summary) return record.summary;
  if (!(record.gzip instanceof ArrayBuffer) || record.gzip.byteLength > maxStoredBytes || typeof DecompressionStream === "undefined") return;
  const input = new ReadableStream<BufferSource>({start(c) { c.enqueue(new Uint8Array(record.gzip!)); c.close(); }});
  const reader = input.pipeThrough(new DecompressionStream("gzip")).getReader();
  const decoder = new TextDecoder();
  const chunks: string[] = [];
  let size = 0;
  try {
    while (true) {
      const {done, value} = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > maxRawBytes) { await reader.cancel(); return; }
      chunks.push(decoder.decode(value, {stream: true}));
    }
    chunks.push(decoder.decode());
    return JSON.parse(chunks.join(""));
  } finally { reader.releaseLock(); }
}
