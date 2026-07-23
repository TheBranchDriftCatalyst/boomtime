import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown } from "lucide-react";
import { QueryGate } from "@/components/QueryGate";
import { PageToolbar } from "@/components/toolbar/PageToolbar";
import { DateRangePicker } from "@/components/toolbar/DateRangePicker";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@thebranchdriftcatalyst/catalyst-ui/ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/dropdown-menu";
import {
  Table,
  TableBody,
  TableCell,
  TableRow,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/table";
import { useTimeRange } from "@/hooks/useTimeRange";
import { api } from "@/lib/api";
import { qk } from "@/lib/queryKeys";
import { secondsToHms } from "@/lib/utils";
import type { LeaderboardEntry } from "@/types/api";

function LeaderboardTable({ users }: { users: LeaderboardEntry[] }) {
  if (users.length === 0) {
    return (
      <p className="py-8 text-center text-sm text-muted-foreground">
        No data available
      </p>
    );
  }
  return (
    <Table>
      <TableBody>
        {users.map((u, i) => (
          <TableRow key={u.name + i}>
            <TableCell className="w-10 font-mono text-muted-foreground">
              {i + 1}
            </TableCell>
            <TableCell className="font-medium">{u.name}</TableCell>
            <TableCell className="text-right text-muted-foreground">
              {secondsToHms(u.value)}
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}

export function Leaderboards() {
  const tr = useTimeRange();
  const [lang, setLang] = useState<string | null>(null);

  const query = useQuery({
    queryKey: qk.leaderboards(tr.startISO, tr.endISO),
    queryFn: () => api.getLeaderboards({ start: tr.startISO, end: tr.endISO }),
  });

  const data = query.data;
  const langs = useMemo(
    () => (data ? Object.keys(data.languages) : []),
    [data],
  );

  useEffect(() => {
    if (!lang && langs.length > 0) setLang(langs[0]);
  }, [langs, lang]);

  return (
    <div>
      <PageToolbar title="Leaderboards">
        <DateRangePicker
          numDays={tr.numDays}
          onPreset={tr.setDaysFromToday}
          onRange={tr.setRange}
        />
      </PageToolbar>

      <QueryGate query={query} errorMessage="Failed to load leaderboards.">
        {(data) => (
        <div className="grid grid-cols-1 gap-6 lg:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle className="text-sm text-muted-foreground">
                Overall time
              </CardTitle>
            </CardHeader>
            <CardContent>
              <LeaderboardTable users={data.global} />
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="flex flex-row items-center justify-between space-y-0">
              <CardTitle className="text-sm text-muted-foreground">
                Language usage{lang ? ` (${lang})` : ""}
              </CardTitle>
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <Button variant="outline" size="sm" disabled={langs.length === 0}>
                    {lang ?? "Language"}
                    <ChevronDown className="h-4 w-4" />
                  </Button>
                </DropdownMenuTrigger>
                <DropdownMenuContent
                  align="end"
                  className="max-h-72 overflow-y-auto"
                >
                  {langs.map((l) => (
                    <DropdownMenuItem key={l} onSelect={() => setLang(l)}>
                      {l}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            </CardHeader>
            <CardContent>
              <LeaderboardTable users={lang ? data.languages[lang] ?? [] : []} />
            </CardContent>
          </Card>
        </div>
        )}
      </QueryGate>
    </div>
  );
}
