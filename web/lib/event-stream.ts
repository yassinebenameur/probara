/** Incrementally parses SSE text; one parser belongs to one HTTP connection. */
export function createEventStreamParser(onEvent: (event: string, data: string) => void) {
  let line = '';
  let event = '';
  let data: string[] = [];
  let skipLF = false;

  function finishLine() {
    if (line === '') {
      if (data.length > 0) {
        onEvent(event || 'message', data.join('\n'));
      }
      event = '';
      data = [];
    } else if (!line.startsWith(':')) {
      const colon = line.indexOf(':');
      const field = colon < 0 ? line : line.slice(0, colon);
      let value = colon < 0 ? '' : line.slice(colon + 1);
      if (value.startsWith(' ')) value = value.slice(1);
      if (field === 'event') event = value;
      if (field === 'data') data.push(value);
    }
    line = '';
  }

  return (chunk: string) => {
    for (const character of chunk) {
      if (skipLF) {
        skipLF = false;
        if (character === '\n') continue;
      }
      if (character === '\r' || character === '\n') {
        finishLine();
        skipLF = character === '\r';
      } else {
        line += character;
      }
    }
  };
}
