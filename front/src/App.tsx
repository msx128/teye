import React, { useState, useEffect } from "react"

type User = {
    name: string,
    id: number,
}

async function postJson(url: string, body: unknown): Promise<Response> {
    return fetch (url, {
        method: "POST", 
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify(body)
    })
}

function Login({onLogin, onSwitch}: { onLogin: (user: User) => void; onSwitch: () => void}) {
    const [name, setName] = useState("")
    const [password, setPassword] = useState("")
    const [error, setError] = useState<string | null>(null)

    async function handleLogin(event:React.SubmitEvent<HTMLFormElement>) { 
        event.preventDefault()
        try {
            const res = await postJson("/auth/login", {name,password})
            if (!res.ok) {
                if (res.status == 400) {
                    setError("Bad request, try to register")
                    return
                } else {
                    setError("Login failed")
                    return
                }
            }
            const user = await fetch("/api/me")
            if (!user.ok) {
                setError("network error")
            }
            onLogin(await user.json())
        } catch (err) {
            setError("Network error")
        }
    }
    return (
        <>
        <h2>Log in</h2>
        <form onSubmit={handleLogin}>
            <label>
                Name
                <input value={name} onChange={(e) => setName(e.target.value)} required />
            </label>
            <label>
                Password
                <input value={password} onChange={(e) => setPassword(e.target.value)} type="password" required />
            </label>
            <button type="submit">log in</button>
        </form>
        <button onClick={onSwitch}>register</button>
        {error && <p>{error}</p>}
        </>
    )
}

function Register({onSwitch}: {onSwitch: () => void}) {
    const [name, setName] = useState("")
    const [password, setPassword] = useState("")
    const [error, setError] = useState<string | null>(null)

    async function handleRegister(event:React.SubmitEvent<HTMLFormElement>) {
        event.preventDefault()
        try {
            const res = await postJson("/auth/register", {name, password})
            if (!res.ok) {
                setError("Failed to register")
            }
        } catch (err) {
            setError("Network error")
        }
    }
    return (
        <>
        <h2>register</h2>
        <form onSubmit={handleRegister}>
            <label>
                Name
                <input value={name} onChange={(e) => setName(e.target.value)} required />
            </label>
            <label>
                Password
                <input value={password} onChange={(e) => setPassword(e.target.value)} type="password" required />
            </label>
            <button type="submit">register</button>
        </form>
        <button onClick={onSwitch}>login</button>
        {error && <p>{error}</p>}
        </>
    )    
}

function AuthPanel({onLogin}: {onLogin: (user: User) => void}) {
    const [state, setState] = useState<"login" | "register">("login")

    return state === "login" ? 
    <Login onLogin={onLogin} onSwitch={() => setState("register")}/> : 
    <Register onSwitch={() => setState("login")}/>
}

function UserBrief({user}: {user: User}) {
    return <p>Welcome {user.name} {user.id}</p>
}

function App() {
    const [user, setUser] = useState<User | null>(null)
    const [loading, setLoading] = useState(true)

    useEffect(() => {
        async function checkUser() {
            try {
                const res = await fetch("/api/me")
                if (res.ok) {
                    setUser(await res.json())
                }
            } catch (e) {
                console.error(e)
            } finally {
                setLoading(false)
            }
        }

        checkUser()
    }, [])

    return (
    <>
        <h1>TEYE</h1>
        <section id="userSec">
            {loading ? (
                <p>Loading...</p>
            ) : user ? (
                <UserBrief user={user} />
            ) : (
                <AuthPanel onLogin={setUser} />
            )}
        </section>
    </>
    )
}

export default App