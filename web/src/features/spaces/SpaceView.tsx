import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router";
import { Check, Pencil, Settings2, Trash2, X } from "lucide-react";
import { toast } from "sonner";
import { OverviewDashboard } from "@/components/OverviewDashboard";
import { SpaceRuleForm } from "@/components/spaces/SpaceRuleForm";
import { Spinner } from "@/components/Spinner";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { axisLabel } from "@/components/heartbeats/axes";
import { useSpace, useSpaceMutations } from "@/hooks/useSpaces";
import type { HeartbeatAxis } from "@/types/api";

export function SpaceView() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const spaceQuery = useSpace(id);
  const { rename, remove, deleteRule } = useSpaceMutations();

  const [managing, setManaging] = useState(false);
  const [editingName, setEditingName] = useState(false);
  const [nameDraft, setNameDraft] = useState("");

  const space = spaceQuery.data;

  const spaceName = space?.name;
  useEffect(() => {
    if (spaceName != null) setNameDraft(spaceName);
  }, [spaceName]);

  // Reset panels when navigating between spaces.
  useEffect(() => {
    setManaging(false);
    setEditingName(false);
  }, [id]);

  if (spaceQuery.isLoading || !space) {
    return <Spinner />;
  }

  function saveName() {
    const next = nameDraft.trim();
    if (!id || !next || next === space?.name) {
      setEditingName(false);
      return;
    }
    rename.mutate(
      { id, name: next },
      {
        onSuccess: () => {
          toast.success("Space renamed");
          setEditingName(false);
        },
        onError: () => toast.error("Failed to rename space"),
      },
    );
  }

  function handleDelete() {
    if (!id) return;
    if (!window.confirm(`Delete the "${space?.name}" space?`)) return;
    remove.mutate(id, {
      onSuccess: () => {
        toast.success("Space deleted");
        navigate("/app");
      },
      onError: () => toast.error("Failed to delete space"),
    });
  }

  const manageButton = (
    <Button
      variant={managing ? "secondary" : "outline"}
      size="sm"
      onClick={() => setManaging((m) => !m)}
    >
      <Settings2 className="h-4 w-4" />
      Manage
    </Button>
  );

  return (
    <div className="space-y-6">
      {managing && (
        <Card>
          <CardHeader className="flex flex-row items-center justify-between space-y-0 pb-2">
            <CardTitle className="text-sm font-semibold text-muted-foreground">
              Manage space
            </CardTitle>
            <Button
              variant="ghost"
              size="sm"
              className="text-destructive hover:text-destructive"
              onClick={handleDelete}
              disabled={remove.isPending}
            >
              <Trash2 className="h-4 w-4" />
              Delete space
            </Button>
          </CardHeader>
          <CardContent className="space-y-4">
            {/* Name editor */}
            <div className="space-y-1">
              <p className="text-xs font-medium text-muted-foreground">Name</p>
              {editingName ? (
                <div className="flex items-center gap-2">
                  <Input
                    value={nameDraft}
                    onChange={(e) => setNameDraft(e.target.value)}
                    className="h-8 max-w-xs"
                    onKeyDown={(e) => {
                      if (e.key === "Enter") saveName();
                      if (e.key === "Escape") setEditingName(false);
                    }}
                    autoFocus
                  />
                  <Button
                    size="icon"
                    className="h-8 w-8"
                    onClick={saveName}
                    disabled={rename.isPending}
                    aria-label="Save name"
                  >
                    <Check className="h-4 w-4" />
                  </Button>
                  <Button
                    variant="secondary"
                    size="icon"
                    className="h-8 w-8"
                    onClick={() => {
                      setNameDraft(space.name);
                      setEditingName(false);
                    }}
                    aria-label="Cancel"
                  >
                    <X className="h-4 w-4" />
                  </Button>
                </div>
              ) : (
                <div className="flex items-center gap-2">
                  <span className="text-sm font-medium">{space.name}</span>
                  <Button
                    variant="ghost"
                    size="icon"
                    className="h-7 w-7"
                    onClick={() => setEditingName(true)}
                    aria-label="Rename space"
                  >
                    <Pencil className="h-3.5 w-3.5" />
                  </Button>
                </div>
              )}
            </div>

            {/* Rules list */}
            <div className="space-y-2">
              <p className="text-xs font-medium text-muted-foreground">Rules</p>
              {space.rules.length === 0 ? (
                <p className="text-sm text-muted-foreground">
                  Add a rule to define what's in this Space.
                </p>
              ) : (
                <ul className="space-y-1">
                  {space.rules.map((rule) => (
                    <li
                      key={rule.id}
                      className="flex items-center gap-2 rounded-md border bg-muted/30 px-2 py-1.5 text-sm"
                    >
                      <span className="text-xs font-medium text-muted-foreground">
                        {axisLabel(rule.axis as HeartbeatAxis)}
                      </span>
                      <span className="rounded bg-secondary px-1.5 py-0.5 text-[10px] font-medium uppercase text-secondary-foreground">
                        {rule.matchType}
                      </span>
                      <span className="flex-1 truncate font-mono" title={rule.matchValue}>
                        {rule.matchValue}
                      </span>
                      <Button
                        variant="ghost"
                        size="icon"
                        className="h-6 w-6 text-muted-foreground hover:text-destructive"
                        aria-label="Delete rule"
                        onClick={() =>
                          id &&
                          deleteRule.mutate(
                            { id, rid: rule.id },
                            {
                              onError: () =>
                                toast.error("Failed to delete rule"),
                            },
                          )
                        }
                      >
                        <X className="h-4 w-4" />
                      </Button>
                    </li>
                  ))}
                </ul>
              )}
            </div>

            {/* Add-rule form (with live preview) */}
            <div className="space-y-1">
              <p className="text-xs font-medium text-muted-foreground">
                Add a rule
              </p>
              {id && <SpaceRuleForm spaceId={id} />}
            </div>
          </CardContent>
        </Card>
      )}

      {space.rules.length === 0 ? (
        <div>
          {/* Keep the toolbar (with Manage) available even before any rule. */}
          <div className="mb-6 flex items-center justify-between">
            <h1 className="text-2xl font-semibold">{space.name}</h1>
            {manageButton}
          </div>
          <Card>
            <CardContent className="flex flex-col items-center gap-3 py-16 text-center">
              <p className="text-lg font-medium">This Space is empty</p>
              <p className="max-w-md text-sm text-muted-foreground">
                Add a rule to define what's in this Space. Its dashboard will
                populate once a rule matches your activity.
              </p>
              <Button onClick={() => setManaging(true)}>
                <Settings2 className="h-4 w-4" />
                Add a rule
              </Button>
            </CardContent>
          </Card>
        </div>
      ) : (
        <OverviewDashboard
          key={`space-${id}`}
          space={id}
          title={space.name}
          toolbarActions={manageButton}
        />
      )}
    </div>
  );
}
