import { useEffect, useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { toast } from "sonner";
import { Badge } from "@thebranchdriftcatalyst/catalyst-ui/ui/badge";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dialog";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { Spinner } from "@/components/Spinner";
import { api } from "@/lib/api";
import { secondsToHms } from "@/lib/utils";
import type { Commit } from "@/types/api";

interface CommitListModalProps {
  project: string | null;
  onClose: () => void;
}

export function CommitListModal({ project, onClose }: CommitListModalProps) {
  const [repoOwner, setRepoOwner] = useState("");
  const [repoName, setRepoName] = useState(project ?? "");
  const [githubUser, setGithubUser] = useState("");

  useEffect(() => {
    setRepoName(project ?? "");
  }, [project]);

  const fetchCommits = useMutation({
    mutationFn: () =>
      api.getCommitLog(project ?? "", {
        repoOwner,
        repoName,
        user: githubUser,
        limit: 80,
      }),
    onError: () => toast.error("Failed to fetch the commit log"),
  });

  function onSubmit(e: React.FormEvent) {
    e.preventDefault();
    fetchCommits.mutate();
  }

  const commits = fetchCommits.data?.commits ?? [];

  return (
    <Dialog open={project !== null} onOpenChange={(o) => !o && onClose()}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Time spent per commit</DialogTitle>
        </DialogHeader>
        <form onSubmit={onSubmit} className="space-y-4">
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
            <div className="space-y-1.5">
              <Label htmlFor="repo-owner">Repo owner</Label>
              <Input
                id="repo-owner"
                required
                value={repoOwner}
                onChange={(e) => setRepoOwner(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="repo-name">Repo name</Label>
              <Input
                id="repo-name"
                required
                value={repoName}
                onChange={(e) => setRepoName(e.target.value)}
              />
            </div>
            <div className="space-y-1.5">
              <Label htmlFor="gh-user">Your GitHub username</Label>
              <Input
                id="gh-user"
                required
                value={githubUser}
                onChange={(e) => setGithubUser(e.target.value)}
              />
            </div>
          </div>

          <div>
            <h5 className="mb-2 text-sm font-semibold">Commits</h5>
            <div className="max-h-[300px] divide-y overflow-y-auto rounded-md border">
              {fetchCommits.isPending ? (
                <Spinner />
              ) : commits.length === 0 ? (
                <p className="p-4 text-center text-sm text-muted-foreground">
                  Nothing fetched yet...
                </p>
              ) : (
                commits.map((c: Commit, i: number) => (
                  <a
                    key={c.html_url + i}
                    href={c.html_url}
                    target="_blank"
                    rel="noreferrer"
                    className="block p-3 transition-colors hover:bg-accent"
                  >
                    <div className="flex items-center justify-between gap-2">
                      <span className="truncate text-sm font-medium">
                        {c.commit.message}
                      </span>
                      <Badge variant="secondary" className="shrink-0">
                        {secondsToHms(c.total_seconds)}
                      </Badge>
                    </div>
                    <p className="mt-1 text-xs text-muted-foreground">
                      {c.author?.login} -{" "}
                      {new Date(c.commit.author.date).toLocaleString()}
                    </p>
                  </a>
                ))
              )}
            </div>
          </div>

          <div className="flex flex-row-reverse gap-2">
            <Button type="submit" disabled={fetchCommits.isPending}>
              Fetch
            </Button>
            <Button type="button" variant="secondary" onClick={onClose}>
              Cancel
            </Button>
          </div>
        </form>
      </DialogContent>
    </Dialog>
  );
}
