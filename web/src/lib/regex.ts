export function isValidRegex(pattern: string) {
    if (pattern === '') return true;

    try {
        new RegExp(pattern);
        return true;
    } catch {
        return false;
    }
}
