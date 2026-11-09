// Границы слова считаются по тексту блока, в том числе через встроенную разметку.
export function wordRange(point) {
  const [node, offset] = point;
  const el = node.nodeType === 3 ? node.parentElement : node;
  const block = el.closest('p, li, h1, h2, h3, h4, h5, h6, td, th, pre, .cl, .tx') || el.closest('[data-p]');
  if (!block) return null;
  const nodes = [];
  const walk = document.createTreeWalker(block, NodeFilter.SHOW_TEXT);
  let text = '', at = -1, n;
  while ((n = walk.nextNode())) {
    if (!n.parentElement.closest('[data-p]') || n.parentElement.closest('.cc, .ced')) continue;
    if (n === node) at = text.length + offset;
    nodes.push({ node: n, start: text.length });
    text += n.nodeValue;
  }
  if (at < 0 || !text.length) return null;
  const segments = [...new Intl.Segmenter(undefined, { granularity: 'word' }).segment(text)];
  const hit = segments.find(s => at >= s.index && at < s.index + s.segment.length) || segments.at(-1);
  const start = hit.index, end = start + hit.segment.length;
  const a = nodes.find(x => start < x.start + x.node.length);
  const b = nodes.find(x => end <= x.start + x.node.length);
  const r = document.createRange();
  r.setStart(a.node, start - a.start);
  r.setEnd(b.node, end - b.start);
  return r;
}

export function extendWords(anchor, word) {
  const r = anchor.cloneRange();
  if (word.compareBoundaryPoints(Range.START_TO_START, anchor) < 0) r.setStart(word.startContainer, word.startOffset);
  if (word.compareBoundaryPoints(Range.END_TO_END, anchor) > 0) r.setEnd(word.endContainer, word.endOffset);
  return r;
}
