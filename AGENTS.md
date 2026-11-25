# UI layout stability

- Do not let asynchronous feedback move existing UI, reflow toolbars or table rows, or cause page-level horizontal overflow. Keep the toolbar and table in place before, during, and after submission or refresh.
- Do not insert temporary receipts, progress messages, or errors into the main layout. Use a brief, non-blocking overlay for routine confirmations, a slot reserved from the initial render for inline indicators, or an accessible dialog for failures and uncertain submissions. Keep long diagnostic text scrollable or wrapped inside its own panel or dialog.
- For changes to async UI feedback, verify success, failure and partial/uncertain submission when applicable. Check both wide and narrow layouts, including long messages and whether the toolbar and table stay in place.
