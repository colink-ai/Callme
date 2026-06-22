interface CandidateDraftLike {
  title?: string;
  question?: string;
  content?: string;
  confidence?: number;
  reason?: string;
}

function stripCodeFence(raw: string) {
  const text = raw.trim();
  if (!text.startsWith('```')) return text;
  return text
    .replace(/^```(?:json|markdown|md)?\s*/i, '')
    .replace(/```\s*$/, '')
    .trim();
}

function unescapeJSONFragment(raw: string) {
  return raw
    .replace(/\\n/g, '\n')
    .replace(/\\r/g, '\r')
    .replace(/\\t/g, '\t')
    .replace(/\\"/g, '"')
    .replace(/\\\\/g, '\\');
}

function extractJSONStringField(raw: string, field: string) {
  const key = `"${field}"`;
  const keyIndex = raw.indexOf(key);
  if (keyIndex < 0) return '';
  const colonIndex = raw.indexOf(':', keyIndex + key.length);
  if (colonIndex < 0) return '';
  const quoteIndex = raw.indexOf('"', colonIndex + 1);
  if (quoteIndex < 0) return '';

  let escaped = false;
  let value = '';
  for (let i = quoteIndex + 1; i < raw.length; i += 1) {
    const ch = raw[i];
    if (escaped) {
      value += `\\${ch}`;
      escaped = false;
      continue;
    }
    if (ch === '\\') {
      escaped = true;
      continue;
    }
    if (ch === '"') break;
    value += ch;
  }
  if (escaped) value += '\\';
  return unescapeJSONFragment(value).trim();
}

function extractJSONNumberField(raw: string, field: string) {
  const match = raw.match(new RegExp(`"${field}"\\s*:\\s*([0-9.]+)`));
  if (!match) return undefined;
  const value = Number(match[1]);
  return Number.isFinite(value) ? value : undefined;
}

function renderCandidateDraft(draft: CandidateDraftLike) {
  const parts: string[] = [];
  if (draft.title) parts.push(`# ${draft.title}`);
  if (draft.question) parts.push(`**建议问题：** ${draft.question}`);
  if (draft.content) parts.push(draft.content);
  if (typeof draft.confidence === 'number') parts.push(`**置信度：** ${Math.round(draft.confidence * 100)}%`);
  if (draft.reason) parts.push(`**生成依据：** ${draft.reason}`);
  return parts.join('\n\n').trim();
}

export function formatAITaskContent(raw: string) {
  const text = stripCodeFence(raw);
  if (!text) return '';
  if (!text.startsWith('{')) return text;

  try {
    const parsed = JSON.parse(text) as CandidateDraftLike;
    const rendered = renderCandidateDraft(parsed);
    if (rendered) return rendered;
  } catch {
    // During streaming the JSON is often incomplete. Fall through to partial extraction.
  }

  const partial = renderCandidateDraft({
    title: extractJSONStringField(text, 'title'),
    question: extractJSONStringField(text, 'question'),
    content: extractJSONStringField(text, 'content'),
    confidence: extractJSONNumberField(text, 'confidence'),
    reason: extractJSONStringField(text, 'reason'),
  });
  return partial || '正在解析结构化结果...';
}
