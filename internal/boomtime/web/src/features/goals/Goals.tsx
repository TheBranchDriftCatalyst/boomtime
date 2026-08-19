// Goals — top-level page (gaka-gud). Promoted from a Settings sub-tab to its
// own left-nav destination as the goal system grows (multiple views,
// punchboard-style trackers, "an attribute over an axis/axis-span"). The tab
// body (GoalsTab: New-goal button + list + the create/edit form) is reused
// verbatim; this file only supplies the Page shell so it stands alone in the
// nav like Overview / Projects.
import { Page } from "@/layout/Page";
import { GoalsTab } from "@boomtime/features/goals/GoalsTab";

export function Goals() {
  return (
    <Page>
      <Page.Header title="Goals" />
      <Page.Body>
        <Page.Content>
          <div className="max-w-3xl">
            <GoalsTab />
          </div>
        </Page.Content>
      </Page.Body>
    </Page>
  );
}
