package minimized;

public class InnerClasses {

  private final int exampleField;

  private static final String STRING = "asdf";

  private static final int top = 5;
  private static final int bottom = 10;

  public InnerClasses(int exampleField) {
    this.exampleField = exampleField;
  }

  public enum InnerEnum {
    A,
    B,
    C
  }

  public interface InnerInterface<A, B> {
    B apply(A a);
  }

  public @interface InnerAnnotation {
    int value();
  }

  @SuppressWarnings(STRING + " ")
  @InnerAnnotation(top / bottom)
  public static class InnerStaticClass {

    public static void innerStaticMethod() {}
  }

  public class InnerClass implements InnerInterface<Integer, Integer> {
    private final int field;

    public InnerClass(int field) {
      this.field = field;
    }

    public void innerMethod() {
      System.out.println(field + exampleField);
    }

    @Override
    public Integer apply(Integer integer) {
      return field * integer;
    }
  }

  private static <A, B> B runInnerInterface(InnerInterface<A, B> fn, A a) {
    return fn.apply(a);
  }

  public static void testEnum(InnerEnum magicEnum) {
    if (System.nanoTime() > System.currentTimeMillis()) {
      magicEnum = InnerEnum.B;
    }
    switch (magicEnum) {
      case B:
        System.out.println("b");
        break;
      case A:
        System.out.println("a");
        break;
      default:
        break;
    }
    if (magicEnum == InnerEnum.A) System.out.println("a");
    else if (magicEnum == InnerEnum.C) System.out.println("b");
    else System.out.println("c");
  }

  public static void testAnon() {
    InnerInterface<String, String> fn =
        new InnerInterface<String, String>() {
          @Override
          public String apply(String s) {
            return s + "b";
          }
        };
    System.out.println(fn.apply("a"));
  }

  public static String app() {
    int a = 42;
    InnerStaticClass.innerStaticMethod();
    InnerClasses innerClasses = new InnerClasses(a);
    InnerClass innerClass = innerClasses.new InnerClass(a);
    innerClass.innerMethod();
    System.out.println(runInnerInterface(innerClass, a));
    testEnum(InnerEnum.A);
    testAnon();
    return "";
  }
}
