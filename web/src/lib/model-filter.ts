export type ModelFilterAction = 'keep' | 'exclude';
export type ModelFilterOperator = 'equals' | 'starts' | 'ends' | 'contains';
export type ModelFilterJoin = 'and' | 'or';

export interface ModelFilterRule {
    action: ModelFilterAction;
    operator: ModelFilterOperator;
    value: string;
    // The first rule has no preceding condition; subsequent joins use AND precedence.
    join: ModelFilterJoin;
}

const patternPrefix = '^(?:';
const patternSuffix = ')[\\s\\S]*$';
const scanPrefix = '[\\s\\S]*';
const exactEnd = '(?![\\s\\S])';

export function escapeModelFilterLiteral(value: string): string {
    const controls: Record<string, string> = { '\n': '\\n', '\r': '\\r', '\t': '\\t', '\f': '\\f', '\v': '\\v' };
    return Array.from(value, char => {
        if (controls[char]) return controls[char];
        const code = char.charCodeAt(0);
        if (code < 32 || code === 127 || code === 0x2028 || code === 0x2029) {
            return `\\u${code.toString(16).padStart(4, '0')}`;
        }
        return char.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
    }).join('');
}

export function compileModelFilter(rules: readonly ModelFilterRule[]): string {
    if (rules.length === 0) return '';

    const branches: string[][] = [];
    for (const rule of rules) {
        if (typeof rule.value !== 'string' || rule.value === '') {
            throw new Error('model filter condition must not be empty');
        }
        if (!['keep', 'exclude'].includes(rule.action)
            || !['equals', 'starts', 'ends', 'contains'].includes(rule.operator)
            || !['and', 'or'].includes(rule.join)) {
            throw new Error('model filter condition is invalid');
        }

        let body = escapeModelFilterLiteral(rule.value);
        if (rule.operator === 'contains' || rule.operator === 'ends') body = scanPrefix + body;
        // $ alone also matches before a final newline; this assertion requires the actual end.
        if (rule.operator === 'equals' || rule.operator === 'ends') body += exactEnd;
        if (branches.length === 0 || rule.join === 'or') branches.push([]);
        branches[branches.length - 1].push(`(?${rule.action === 'keep' ? '=' : '!'}${body})`);
    }

    return patternPrefix + branches.map(branch => branch.join('')).join('|') + patternSuffix;
}

function decodeLiteral(text: string): string | null {
    const controls: Record<string, string> = { n: '\n', r: '\r', t: '\t', f: '\f', v: '\v' };
    let value = '';
    for (let index = 0; index < text.length; index++) {
        if (text[index] !== '\\') {
            value += text[index];
            continue;
        }
        const escaped = text[++index];
        if (escaped === undefined) return null;
        if (escaped === 'u') {
            const hex = text.slice(index + 1, index + 5);
            if (!/^[0-9a-f]{4}$/.test(hex)) return null;
            value += String.fromCharCode(Number.parseInt(hex, 16));
            index += 4;
        } else {
            value += controls[escaped] ?? escaped;
        }
    }
    return escapeModelFilterLiteral(value) === text ? value : null;
}

// Accept only the generator's grammar and exact bytes. Arbitrary user regexes remain raw.
export function parseModelFilter(pattern: string): ModelFilterRule[] | null {
    if (pattern === '') return [];
    if (!pattern.startsWith(patternPrefix) || !pattern.endsWith(patternSuffix)) return null;

    const body = pattern.slice(patternPrefix.length, -patternSuffix.length);
    const rules: ModelFilterRule[] = [];
    let cursor = 0;
    let join: ModelFilterJoin = 'and';

    while (cursor < body.length) {
        const marker = body.slice(cursor, cursor + 3);
        if (marker !== '(?=' && marker !== '(?!') return null;
        const start = cursor;
        let depth = 0;
        let inClass = false;

        for (; cursor < body.length; cursor++) {
            const char = body[cursor];
            if (char === '\\') {
                cursor++;
                continue;
            }
            if (char === '[') inClass = true;
            if (char === ']') inClass = false;
            if (inClass) continue;
            if (char === '(') depth++;
            if (char === ')' && --depth === 0) {
                cursor++;
                break;
            }
        }
        if (depth !== 0) return null;

        let literal = body.slice(start + 3, cursor - 1);
        const scan = literal.startsWith(scanPrefix);
        const end = literal.endsWith(exactEnd);
        if (scan) literal = literal.slice(scanPrefix.length);
        if (end) literal = literal.slice(0, -exactEnd.length);
        const value = decodeLiteral(literal);
        if (value === null || value === '') return null;

        rules.push({
            action: marker === '(?=' ? 'keep' : 'exclude',
            operator: scan ? (end ? 'ends' : 'contains') : (end ? 'equals' : 'starts'),
            value,
            join,
        });
        join = 'and';
        if (body[cursor] === '|') {
            join = 'or';
            cursor++;
            if (cursor === body.length) return null;
        }
    }

    try {
        return rules.length > 0 && compileModelFilter(rules) === pattern ? rules : null;
    } catch {
        return null;
    }
}
