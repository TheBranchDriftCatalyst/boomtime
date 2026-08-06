import { useEffect } from "react";
import { useForm } from "react-hook-form";
import { zodResolver } from "@hookform/resolvers/zod";
import { z } from "zod";
import { Button } from "@thebranchdriftcatalyst/catalyst-ui/ui/button";
import { Input } from "@thebranchdriftcatalyst/catalyst-ui/ui/input";
import { Textarea } from "@thebranchdriftcatalyst/catalyst-ui/ui/textarea";
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
import { useAnnotationContext } from "@/features/devtools/context";
import type { ComponentInfo } from "./ComponentInspector";

const annotationSchema = z.object({
  componentName: z.string().min(1, "Component name is required"),
  note: z.string().min(1, "Note is required"),
  type: z.enum(["todo", "bug", "note", "docs"]),
  priority: z.enum(["low", "medium", "high"]),
});

type AnnotationFormValues = z.infer<typeof annotationSchema>;

interface AnnotationFormProps {
  /**
   * Callback fired when annotation is successfully created
   */
  onSuccess?: () => void;
  /**
   * Pre-fill component name
   */
  defaultComponentName?: string;
  /**
   * Selected component from inspector
   */
  selectedComponent?: ComponentInfo | null;
}

/**
 * Form for creating new annotations
 */
export function AnnotationForm({
  onSuccess,
  defaultComponentName,
  selectedComponent,
}: AnnotationFormProps) {
  const { addAnnotation } = useAnnotationContext();

  const form = useForm<AnnotationFormValues>({
    resolver: zodResolver(annotationSchema),
    defaultValues: {
      componentName: defaultComponentName || "",
      note: "",
      type: "note",
      priority: "medium",
    },
  });

  // Update form when component is selected via inspector
  useEffect(() => {
    if (selectedComponent) {
      form.setValue("componentName", selectedComponent.name);
    }
  }, [selectedComponent, form]);

  const onSubmit = (values: AnnotationFormValues) => {
    addAnnotation(values);
    form.reset({
      componentName: defaultComponentName || "",
      note: "",
      type: "note",
      priority: "medium",
    });
    onSuccess?.();
  };

  return (
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
                Type the name of the component this annotation is for
              </FormDescription>
              <FormMessage />
            </FormItem>
          )}
        />

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
  );
}
