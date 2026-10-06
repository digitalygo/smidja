# Transcript search

Transcript search indexes the published fullscreen frame, not live scroll state, so a query change always rebuilds against the frame that is currently on screen.

## Bracketed paste budget

The search overlay accepts bracketed paste content through the same query budget as typed input, 256 bytes. Content beyond that byte budget is dropped at a rune boundary, and the paste end marker is still detected when it is split across reads. Closing the search, replacing the session, and reopening the search all reset the paste lifecycle, so an interrupted paste never carries into the next search session.

## Known limitation

The index joins adjacent visual lines with a single space so that a query can match across a word wrap. A match that spans a hard wrap in the middle of a word, or a boundary where an empty line separates two unrelated lines, is therefore not guaranteed to match. The join never concatenates unrelated text, so a false positive across empty lines is not possible.
