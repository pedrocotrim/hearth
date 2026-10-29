plugins {
    id("com.android.application")
}

android {
    namespace = "app.hearth"
    compileSdk = 35

    defaultConfig {
        applicationId = "app.hearth"
        minSdk = 26
        targetSdk = 35
        versionCode = 1
        versionName = "1.0"
    }

    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
}

dependencies {
    implementation(files("libs/hearth.aar"))
}
