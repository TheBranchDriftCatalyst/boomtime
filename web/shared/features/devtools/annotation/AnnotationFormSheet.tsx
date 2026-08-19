import { useEffect } from "react";
import { useForm, useWatch } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetHeader,
  SheetTitle,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/sheet";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Textarea } from "@thebranchdriftcatalyst/catalyst-ui/ui/textarea";
import { RadioGroup, RadioGroupItem } from "@thebranchdriftcatalyst/catalyst-ui/ui/radio-group";
import { Label } from "@thebranchdriftcatalyst/catalyst-ui/ui/label";
import { Badge } from "@thebranchdriftcatalyst/catalyst-ui/ui/badge";
import {
  Form,
  FormControl,
  FormDescription,
  FormField,
  FormItem,
  FormLabel,
  FormMessage,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/form";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@thebranchdriftcatalyst/catalyst-ui/ui/select";
import { useAnnotationContext } from "@shared/features/devtools/context";
import type { ComponentInfo } from "./ComponentInspector";

const annotationSchema = z.object({
  componentName: z.string().min(1, "Component name is required"),
  note: z.string().min(1, "Note is required"),
  type: z.enum(["todo", "bug", "note", "docs"]),
  priority: z.enum(["low", "medium", "high"]),
  scope: z.enum(["instance", "class"]),
});

type AnnotationFormValues = z.infer<typeof annotationSchema>;

interface AnnotationFormSheetProps {
  /**
   * Whether the sheet is open
   */
  open: boolean;
  /**
   * Callback when sheet should close
   */
  onOpenChange: (open: boolean) => void;
  /**
   * Selected component from inspector
   */
  selectedComponent?: ComponentInfo | null;
}

/**
 * Bottom sheet for creating new annotations
 *
 * Features:
 * - Pre-filled component identifiers from inspector
 * - Scope selection: "This instance" vs "All instances"
 * - Form validation
 */
export function AnnotationFormSheet({
  open,
  onOpenChange,
  selectedComponent,
}: AnnotationFormSheetProps) {
  const { addAnnotation } = useAnnotationContext();

  const form = useForm<AnnotationFormValues>({
    resolver: zodResolver(annotationSchema),
    defaultValues: {
      componentName: "",
      note: "",
      type: "note",
      priority: "medium",
      scope: "instance",
    },
  });

  // Update form when component is selected via inspector
  useEffect(() => {
    if (selectedComponent) {
      form.setValue("componentName", selectedComponent.name);
    }
  }, [selectedComponent, form]);

  const onSubmit = (values: AnnotationFormValues) => {
    addAnnotation({
      componentName: values.componentName,
      note: values.note,
      type: values.type,
      priority: values.priority,
      // Include location info only for instance-scoped annotations
      ...(values.scope === "instance" && selectedComponent
        ? {
            // New identifiers (preferred)
            instanceId: selectedComponent.instanceId,
            treePath: selectedComponent.treePath,
            // Legacy identifiers (fallback)
            filePath: selectedComponent.filePath,
            lineNumber: selectedComponent.lineNumber,
          }
        : {}),
    });

    form.reset({
      componentName: "",
      note: "",
      type: "note",
      priority: "medium",
      scope: "instance",
    });

    onOpenChange(false);
  };

  // Watch form values for reactive identifier preview
  const scopeValue = useWatch({ control: form.control, name: "scope" });
  const componentNameValue = useWatch({ control: form.control, name: "componentName" });

  return (
    <Sheet open={open} onOpenChange={onOpenChange}>
      <SheetContent side="bottom" className="h-[70vh] overflow-y-auto">
        <SheetHeader>
          <SheetTitle>Create Annotation</SheetTitle>
          <SheetDescription>Add a note, TODO, or bug report for this component</SheetDescription>
        </SheetHeader>

        <div className="mt-6 space-y-6">
          {/* Component Info Display */}
          {selectedComponent && (
            <div className="rounded-lg border p-4 space-y-3 bg-muted/50">
              <div className="flex items-center justify-between">
                <h4 className="text-sm font-semibold">{selectedComponent.name}</h4>
                <Badge variant="secondary" className="text-xs">
                  {selectedComponent.type}
                </Badge>
              </div>

              {selectedComponent.treePath && (
                <div className="text-xs text-muted-foreground">
                  <strong>Tree Path:</strong> {selectedComponent.treePath}
                </div>
              )}

              {selectedComponent.instanceId && (
                <div className="text-xs text-muted-foreground font-mono">
                  <strong>Instance ID:</strong> {selectedComponent.instanceId}
                </div>
              )}

              {selectedComponent.filePath && (
                <div className="text-xs text-muted-foreground">
                  <strong>File:</strong> {selectedComponent.filePath.replace(/^.*\//, "")}
                  {selectedComponent.lineNumber && `:${selectedComponent.lineNumber}`}
                </div>
              )}

              {Object.keys(selectedComponent.props).length > 0 && (
                <details className="text-xs">
                  <summary className="cursor-pointer font-medium text-muted-foreground">
                    Props ({Object.keys(selectedComponent.props).length})
                  </summary>
                  <pre className="mt-2 bg-muted p-2 rounded max-h-32 overflow-auto">
                    {JSON.stringify(
                      Object.fromEntries(
                        Object.entries(selectedComponent.props).filter(
                          ([key]) => !key.startsWith("_") && key !== "children"
                        )
                      ),
                      null,
                      2
                    )}
                  </pre>
                </details>
              )}
            </div>
          )}

          <Form {...form}>
            <form onSubmit={form.handleSubmit(onSubmit)} className="space-y-4">
              <FormField
                control={form.control}
                name="componentName"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Component Name</FormLabel>
                    <FormControl>
                      <Input placeholder="e.g., MyComponent" {...field} />
                    </FormControl>
                    <FormDescription>
                      The name of the component this annotation is for
                    </FormDescription>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {/* Scope Selection */}
              <FormField
                control={form.control}
                name="scope"
                render={({ field }) => (
                  <FormItem className="space-y-3">
                    <FormLabel>Annotation Scope</FormLabel>
                    <FormControl>
                      <RadioGroup
                        onValueChange={field.onChange}
                        value={field.value}
                        className="flex flex-col space-y-1"
                      >
                        <div className="flex items-center space-x-2">
                          <RadioGroupItem value="instance" id="instance" />
                          <Label htmlFor="instance" className="font-normal cursor-pointer">
                            <span className="font-medium">This instance</span>
                            {selectedComponent?.instanceId ? (
                              <span className="text-xs text-muted-foreground ml-2 font-mono">
                                (#{selectedComponent.instanceId})
                              </span>
                            ) : (
                              selectedComponent?.filePath &&
                              selectedComponent?.lineNumber && (
                                <span className="text-xs text-muted-foreground ml-2">
                                  (line {selectedComponent.lineNumber})
                                </span>
                              )
                            )}
                            <p className="text-xs text-muted-foreground mt-1">
                              Annotate this specific occurrence of the component
                            </p>
                          </Label>
                        </div>
                        <div className="flex items-center space-x-2">
                          <RadioGroupItem value="class" id="class" />
                          <Label htmlFor="class" className="font-normal cursor-pointer">
                            <span className="font-medium">All instances of this component</span>
                            <p className="text-xs text-muted-foreground mt-1">
                              Annotate the component class (applies to all uses)
                            </p>
                          </Label>
                        </div>
                      </RadioGroup>
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              {/* Identifier Display */}
              <div className="rounded-lg border border-muted bg-muted/30 p-3 space-y-2">
                <div className="text-xs font-medium text-muted-foreground">Identifier Preview</div>
                <div className="font-mono text-xs break-all">
                  {scopeValue === "instance" ? (
                    // Instance scope: show instance ID or file/line
                    <div className="space-y-1">
                      <div>
                        <span className="text-muted-foreground">Component:</span>{" "}
                        <span className="text-foreground font-medium">
                          {componentNameValue || "Component"}
                        </span>
                      </div>
                      {selectedComponent?.treePath && (
                        <div>
                          <span className="text-muted-foreground">Path:</span>{" "}
                          <span className="text-foreground text-[10px]">
                            {selectedComponent.treePath}
                          </span>
                        </div>
                      )}
                      {selectedComponent?.instanceId ? (
                        <>
                          <div>
                            <span className="text-muted-foreground">Instance:</span>{" "}
                            <span className="text-foreground">{selectedComponent.instanceId}</span>
                          </div>
                          <div className="mt-2 pt-2 border-t border-muted">
                            <span className="text-blue-600 dark:text-blue-400 font-medium">
                              {componentNameValue || "Component"}#{selectedComponent.instanceId}
                            </span>
                          </div>
                        </>
                      ) : selectedComponent?.filePath && selectedComponent?.lineNumber ? (
                        <>
                          <div>
                            <span className="text-muted-foreground">File:</span>{" "}
                            <span className="text-foreground">
                              {selectedComponent.filePath.replace(/^.*\//, "")}
                            </span>
                          </div>
                          <div>
                            <span className="text-muted-foreground">Line:</span>{" "}
                            <span className="text-foreground">{selectedComponent.lineNumber}</span>
                          </div>
                          <div className="mt-2 pt-2 border-t border-muted">
                            <span className="text-blue-600 dark:text-blue-400 font-medium">
                              {componentNameValue || "Component"}@
                              {selectedComponent.filePath.replace(/^.*\//, "")}:
                              {selectedComponent.lineNumber}
                            </span>
                          </div>
                        </>
                      ) : (
                        <div className="mt-2 pt-2 border-t border-muted">
                          <span className="text-blue-600 dark:text-blue-400 font-medium">
                            {componentNameValue || "Component"}@(instance)
                          </span>
                          <p className="text-xs text-muted-foreground mt-1">
                            No identifier available
                          </p>
                        </div>
                      )}
                    </div>
                  ) : (
                    // Class scope: show wildcard
                    <div className="space-y-1">
                      <div>
                        <span className="text-muted-foreground">Component:</span>{" "}
                        <span className="text-foreground font-medium">
                          {componentNameValue || "Component"}
                        </span>
                      </div>
                      <div>
                        <span className="text-muted-foreground">Scope:</span>{" "}
                        <span className="text-foreground">All instances</span>
                      </div>
                      <div className="mt-2 pt-2 border-t border-muted">
                        <span className="text-purple-600 dark:text-purple-400 font-medium">
                          {componentNameValue || "Component"}@*
                        </span>
                      </div>
                    </div>
                  )}
                </div>
              </div>

              <FormField
                control={form.control}
                name="note"
                render={({ field }) => (
                  <FormItem>
                    <FormLabel>Note</FormLabel>
                    <FormControl>
                      <Textarea
                        placeholder="Add your note here..."
                        className="min-h-[100px] resize-none"
                        {...field}
                      />
                    </FormControl>
                    <FormMessage />
                  </FormItem>
                )}
              />

              <div className="grid grid-cols-2 gap-4">
                <FormField
                  control={form.control}
                  name="type"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Type</FormLabel>
                      <Select onValueChange={field.onChange} defaultValue={field.value}>
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue placeholder="Select type" />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent>
                          <SelectItem value="todo">TODO</SelectItem>
                          <SelectItem value="bug">Bug</SelectItem>
                          <SelectItem value="note">Note</SelectItem>
                          <SelectItem value="docs">Docs</SelectItem>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />

                <FormField
                  control={form.control}
                  name="priority"
                  render={({ field }) => (
                    <FormItem>
                      <FormLabel>Priority</FormLabel>
                      <Select onValueChange={field.onChange} defaultValue={field.value}>
                        <FormControl>
                          <SelectTrigger>
                            <SelectValue placeholder="Select priority" />
                          </SelectTrigger>
                        </FormControl>
                        <SelectContent>
                          <SelectItem value="low">Low</SelectItem>
                          <SelectItem value="medium">Medium</SelectItem>
                          <SelectItem value="high">High</SelectItem>
                        </SelectContent>
                      </Select>
                      <FormMessage />
                    </FormItem>
                  )}
                />
              </div>

              <Button type="submit" className="w-full">
                Add Annotation
              </Button>
            </form>
          </Form>
        </div>
      </SheetContent>
    </Sheet>
  );
}
