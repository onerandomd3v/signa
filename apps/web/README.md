# Signa web styling

- Build layouts and component styles with Tailwind CSS v4 utilities.
- Keep `app/globals.css` for imports, Signa design tokens, resets, and shared base styles.
- Keep shared layout concerns in `AppShell`; keep route-specific layout and typography with that route.
- Add shadcn components only when a feature needs them. Use the configured aliases and `cn()` helper from `lib/utils.ts` for conditional or conflicting classes.
- Use `motion-safe:` and `motion-reduce:` variants for motion so reduced-motion preferences are respected.
