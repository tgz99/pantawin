# Pantawin release rules. Most libraries (Ktor, OkHttp, Coil, Firebase,
# Credential Manager) ship consumer rules; what's here is only what R8 can't
# know on its own.

# kotlinx.serialization: keep the plugin-generated serializers for our API
# models — the JSON (de)serialization of every server response depends on
# them surviving minification.
-keepattributes *Annotation*, InnerClasses
-keep,includedescriptorclasses class com.pantawin.**$$serializer { *; }
-keepclassmembers class com.pantawin.** {
    *** Companion;
}
-keepclasseswithmembers class com.pantawin.** {
    kotlinx.serialization.KSerializer serializer(...);
}

# Ktor references optional/JVM-only classes that don't exist on Android.
-dontwarn org.slf4j.**
-dontwarn java.lang.management.**
