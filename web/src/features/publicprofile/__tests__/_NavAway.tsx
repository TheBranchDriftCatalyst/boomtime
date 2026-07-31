// Tiny helper: a react-router Link used by the ProfileEditor blocker
// test. Extracted so the test file doesn't need to import react-router
// separately (keeps the arrange step readable).
import { Link } from "react-router";

export function NavAway() {
  return (
    <Link to="/somewhere-else" data-testid="nav-away-link">
      Go elsewhere
    </Link>
  );
}
